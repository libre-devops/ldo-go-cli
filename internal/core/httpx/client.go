// Package httpx is the JSON client every API call goes through: bearer auth, bounded
// retries, Retry-After, paging.
//
// Retries are decided on the HTTP status code (408, 429, 5xx) and on connection errors
// or timeouts, never on the wording of an error message. The bearer token is only ever
// sent to the client's own https host, and redirects are not followed. Every POST this
// tool makes is a read-only query or a token request (or, behind an explicit --write, a
// task raised), so retrying one is safe. A 401 is retried once with a fresh token, when
// the token source can drop the one it cached.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Object is a decoded JSON object.
type Object = map[string]any

var retryStatuses = map[int]bool{408: true, 429: true, 500: true, 502: true, 503: true, 504: true}

// UserAgent is sent with every request.
func UserAgent() string { return brand.Command + "/" + brand.Version }

// TokenSource gives the Authorization header's token each time it is asked. Refresh, when
// set, drops a cached token after a 401.
type TokenSource struct {
	Token   func(ctx context.Context) (string, error)
	Refresh func()
}

// Options configure a Client. Only BaseURL is required.
type Options struct {
	BaseURL string
	// Token is nil for an API that takes no Authorization header.
	Token *TokenSource
	// Name is how errors name the API: "Microsoft Graph".
	Name string
	// HTTPClient sends the requests; tests give a fake transport. Its CheckRedirect is
	// replaced, so no redirect is followed.
	HTTPClient *http.Client
	// AuthScheme is "Bearer" when empty; "Basic" when Token gives base64 "user:password".
	AuthScheme string
	// AllowHTTP permits a plain http base URL on a loopback or link-local host, when there
	// is no token: the managed identity endpoints.
	AllowHTTP bool
	// ErrorHints are what to tell a person about an error code this API is known to give,
	// or a status ("HTTP 401") when its errors carry no code.
	ErrorHints map[string]string
	Timeout    time.Duration
	// MaxAttempts is 4 when zero.
	MaxAttempts   int
	Backoff       time.Duration
	MaxBackoff    time.Duration
	MaxRetryAfter time.Duration
	// Sleep waits between attempts; tests give their own.
	Sleep func(time.Duration)
	// Now is time.Now when nil, for Retry-After dates.
	Now func() time.Time
}

// Client is a JSON client for one API base URL.
type Client struct {
	opts   Options
	base   *url.URL
	origin string
	http   *http.Client
}

// New is a client for opts.BaseURL. A base URL that is not https (or, with AllowHTTP and
// no token, http on this machine) is a library caller's mistake: an error.
func New(opts Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(opts.BaseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, fmt.Errorf("base URL must be an https URL, got %q", opts.BaseURL)
	}
	if base.Scheme == "http" && !(opts.AllowHTTP && opts.Token == nil && isLocal(base.Hostname())) {
		return nil, fmt.Errorf("base URL must be an https URL, got %q", opts.BaseURL)
	}
	if opts.Name == "" {
		opts.Name = "API"
	}
	if opts.AuthScheme == "" {
		opts.AuthScheme = "Bearer"
	}
	defaults(&opts)
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	// A copy, so the caller's client keeps its own redirect rule.
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{opts: opts, base: base, origin: origin(base), http: &copied}, nil
}

func defaults(opts *Options) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 4
	}
	if opts.Backoff == 0 {
		opts.Backoff = time.Second
	}
	if opts.MaxBackoff == 0 {
		opts.MaxBackoff = 30 * time.Second
	}
	if opts.MaxRetryAfter == 0 {
		opts.MaxRetryAfter = 120 * time.Second
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
}

// EnsureToken gets the token now, on this goroutine. Call it before fanning requests out
// to workers, so a credential that has to ask someone to sign in again asks once, here.
func (c *Client) EnsureToken(ctx context.Context) error {
	if c.opts.Token == nil {
		return nil
	}
	_, err := c.opts.Token.Token(ctx)
	return err
}

// Name is how errors name this API.
func (c *Client) Name() string { return c.opts.Name }

// BaseURL is the API's base URL, without a trailing slash.
func (c *Client) BaseURL() string { return c.base.String() }

// URL is the absolute URL for path with params percent-encoded.
//
// path may be a full URL (an @odata.nextLink); it must use this client's scheme, host and
// port, so a token never travels anywhere else. The scheme's own port is the same as
// none: Resource Manager's next links name :443.
func (c *Client) URL(path string, params url.Values) (string, error) {
	address := ""
	if strings.Contains(path, "://") {
		parsed, err := url.Parse(path)
		if err != nil || parsed.User != nil || origin(parsed) != c.origin {
			shown := path
			if err == nil {
				shown = parsed.Scheme + "://" + parsed.Host
			}
			return "", errs.APIf("%s: refusing to send a token to %s", c.opts.Name, shown)
		}
		address = path
	} else {
		address = c.base.String() + "/" + strings.TrimLeft(path, "/")
	}
	if len(params) > 0 {
		separator := "?"
		if strings.Contains(address, "?") {
			separator = "&"
		}
		address += separator + strings.ReplaceAll(params.Encode(), "+", "%20")
	}
	return address, nil
}

// Call is one request's details beyond its method and path.
type Call struct {
	Params  url.Values
	Headers map[string]string
	// JSON is a body to send as JSON; Form a body to send form-encoded.
	JSON any
	Form url.Values
	// AllowEmpty accepts a success with no body (some actions answer 204), as {}.
	AllowEmpty bool
}

// Do sends one request and returns the JSON object it responds with.
func (c *Client) Do(ctx context.Context, method, path string, call Call) (Object, error) {
	address, err := c.URL(path, call.Params)
	if err != nil {
		return nil, err
	}
	body, status, err := c.send(ctx, method, address, call)
	if err != nil {
		return nil, err
	}
	if call.AllowEmpty && len(bytes.TrimSpace(body)) == 0 {
		return Object{}, nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		apiErr := errs.APIf("%s: %s %s did not return JSON", c.opts.Name, method, pathOf(address))
		apiErr.Status = status
		return nil, apiErr
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, errs.APIf("%s: %s %s did not return a JSON object", c.opts.Name, method, pathOf(address))
	}
	return object, nil
}

// Get is a GET of path, and the JSON object it responds with.
func (c *Client) Get(ctx context.Context, path string, params url.Values) (Object, error) {
	return c.Do(ctx, http.MethodGet, path, Call{Params: params})
}

// Post is a POST of body as JSON to path, and the JSON object it responds with.
func (c *Client) Post(ctx context.Context, path string, body any, params url.Values) (Object, error) {
	return c.Do(ctx, http.MethodPost, path, Call{Params: params, JSON: body})
}

// GetText is a GET of path, and its body as text, for the few APIs that answer in text.
func (c *Client) GetText(ctx context.Context, path string, params url.Values) (string, error) {
	return c.Text(ctx, path, Call{Params: params})
}

// Text is a GET of path with a call's options (an Accept header, say), and its body as
// text.
func (c *Client) Text(ctx context.Context, path string, call Call) (string, error) {
	address, err := c.URL(path, call.Params)
	if err != nil {
		return "", err
	}
	body, _, err := c.send(ctx, http.MethodGet, address, Call{Headers: call.Headers})
	return string(body), err
}

// All yields every item of a paged collection, following nextLink: "@odata.nextLink" for
// Graph and Defender, "nextLink" for Resource Manager and Key Vault. Pages are fetched
// only as items are consumed; an error ends the sequence.
func (c *Client) All(ctx context.Context, path string, params url.Values, nextLink string) iter.Seq2[Object, error] {
	return c.Pages(ctx, path, Call{Params: params}, nextLink)
}

// Pages is All with a call's headers, which every page is fetched with (Graph's advanced
// queries want ConsistencyLevel on each).
func (c *Client) Pages(ctx context.Context, path string, call Call, nextLink string) iter.Seq2[Object, error] {
	if nextLink == "" {
		nextLink = "@odata.nextLink"
	}
	return func(yield func(Object, error) bool) {
		page, err := c.Do(ctx, http.MethodGet, path, call)
		for {
			if err != nil {
				yield(nil, err)
				return
			}
			items, ok := page["value"].([]any)
			if !ok {
				yield(nil, errs.APIf("%s: response has no 'value' array", c.opts.Name))
				return
			}
			for _, item := range items {
				if object, isObject := item.(map[string]any); isObject && !yield(object, nil) {
					return
				}
			}
			link, _ := page[nextLink].(string)
			if link == "" {
				return
			}
			page, err = c.Do(ctx, http.MethodGet, link, Call{Headers: call.Headers})
		}
	}
}

// Collect is every item All yields, or the first error.
func Collect(seq iter.Seq2[Object, error]) ([]Object, error) {
	var found []Object
	for item, err := range seq {
		if err != nil {
			return nil, err
		}
		found = append(found, item)
	}
	return found, nil
}

func (c *Client) send(ctx context.Context, method, address string, call Call) ([]byte, int, error) {
	payload, contentType, err := encodeBody(call)
	if err != nil {
		return nil, 0, err
	}
	refreshed := false
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
		request, err := c.newRequest(attemptCtx, method, address, payload, contentType, call.Headers)
		if err != nil {
			cancel()
			return nil, 0, err
		}
		response, err := c.http.Do(request)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			if !retryable(err) {
				return nil, 0, errs.APIf("%s: %s %s failed: %v", c.opts.Name, method, pathOf(address), err)
			}
			if attempt >= c.opts.MaxAttempts {
				return nil, 0, errs.APIf("%s: %s %s failed after %d attempts: %v",
					c.opts.Name, method, pathOf(address), attempt, err)
			}
			c.wait(attempt, -1, "connection error")
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		cancel()
		if readErr != nil {
			return nil, response.StatusCode, errs.APIf("%s: %s %s: %v", c.opts.Name, method, pathOf(address), readErr)
		}
		status := response.StatusCode
		if status >= 200 && status < 300 {
			return body, status, nil
		}
		if status == http.StatusUnauthorized && c.opts.Token != nil && c.opts.Token.Refresh != nil && !refreshed {
			// Once only: a second 401 means the token is not the problem.
			refreshed = true
			attempt--
			slog.Info("HTTP 401, retrying once with a new token", "api", c.opts.Name)
			c.opts.Token.Refresh()
			continue
		}
		if retryStatuses[status] && attempt < c.opts.MaxAttempts {
			c.wait(attempt, RetryAfter(response.Header.Get("Retry-After"), c.opts.Now()), fmt.Sprintf("HTTP %d", status))
			continue
		}
		return nil, status, FromResponse(c.opts.Name, response, body, c.opts.ErrorHints)
	}
}

func (c *Client) newRequest(ctx context.Context, method, address string, payload []byte, contentType string, headers map[string]string) (*http.Request, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return nil, errs.APIf("%s: %s %s: %v", c.opts.Name, method, pathOf(address), err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", UserAgent())
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if c.opts.Token != nil {
		token, err := c.opts.Token.Token(ctx)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", c.opts.AuthScheme+" "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return request, nil
}

func encodeBody(call Call) ([]byte, string, error) {
	switch {
	case call.JSON != nil:
		payload, err := json.Marshal(call.JSON)
		if err != nil {
			return nil, "", fmt.Errorf("cannot encode the request body: %w", err)
		}
		return payload, "application/json", nil
	case call.Form != nil:
		return []byte(call.Form.Encode()), "application/x-www-form-urlencoded", nil
	}
	return nil, "", nil
}

func retryable(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) || errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(err.Error(), "connection reset")
}

func (c *Client) wait(attempt int, retryAfter time.Duration, reason string) {
	// A server-directed Retry-After wins over the backoff (retrying earlier just throttles
	// again), capped so a broken server cannot stall the run.
	var delay time.Duration
	if retryAfter >= 0 {
		delay = min(c.opts.MaxRetryAfter, retryAfter)
	} else {
		delay = min(c.opts.MaxBackoff, c.opts.Backoff*time.Duration(1<<(attempt-1)))
		// Jitter, so clients that failed together do not retry together.
		delay += time.Duration(rand.Int64N(int64(c.opts.Backoff/2) + 1))
	}
	slog.Warn(fmt.Sprintf("%s on attempt %d of %d, retrying in %.1fs", reason, attempt, c.opts.MaxAttempts, delay.Seconds()),
		"api", c.opts.Name)
	c.opts.Sleep(delay)
}

// RetryAfter is the wait a Retry-After header asks for (delta-seconds or an HTTP date),
// or -1 when it asks for none.
func RetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return -1
	}
	var seconds int
	if _, err := fmt.Sscanf(value, "%d", &seconds); err == nil && fmt.Sprint(seconds) == value {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return -1
	}
	return max(0, when.Sub(now))
}

var defaultPorts = map[string]string{"https": "443", "http": "80"}

// origin is a URL's scheme, host (lower case) and port, the scheme's own port when it
// names none, so https://host:443 and https://host are the same place.
func origin(address *url.URL) string {
	port := address.Port()
	if port == "" {
		port = defaultPorts[address.Scheme]
	}
	return address.Scheme + "://" + strings.ToLower(address.Hostname()) + ":" + port
}

func isLocal(host string) bool {
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && (address.IsLoopback() || address.IsLinkLocalUnicast())
}

func pathOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return address
	}
	return parsed.Path
}

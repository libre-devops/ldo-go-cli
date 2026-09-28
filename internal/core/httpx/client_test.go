package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

type tokens struct {
	issued    []string
	refreshed int
}

func (t *tokens) source() *TokenSource {
	return &TokenSource{
		Token: func(context.Context) (string, error) {
			token := "token-" + string(rune('a'+len(t.issued)))
			t.issued = append(t.issued, token)
			return token, nil
		},
		Refresh: func() { t.refreshed++ },
	}
}

func client(t *testing.T, handler httpfake.Handler, opts Options) (*Client, *httpfake.Transport, *[]time.Duration) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	var slept []time.Duration
	opts.HTTPClient = httpClient
	opts.Sleep = func(d time.Duration) { slept = append(slept, d) }
	if opts.BaseURL == "" {
		opts.BaseURL = "https://graph.example.test"
	}
	if opts.Name == "" {
		opts.Name = "Graph"
	}
	built, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return built, transport, &slept
}

func TestGetSendsTheTokenAndReadsJSON(t *testing.T) {
	source := &tokens{}
	c, transport, _ := client(t, func(*http.Request) httpfake.Reply {
		return httpfake.JSON(map[string]any{"id": "1", "count": 3})
	}, Options{Token: source.source()})
	got, err := c.Get(context.Background(), "/v1.0/me", url.Values{"$select": {"id,displayName"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["id"] != "1" || got["count"] != float64(3) {
		t.Fatalf("got %v", got)
	}
	seen := transport.Seen()[0]
	if seen.Header.Get("Authorization") != "Bearer token-a" || seen.Header.Get("User-Agent") == "" {
		t.Fatalf("headers %v", seen.Header)
	}
	if seen.URL.RawQuery != "%24select=id%2CdisplayName" {
		t.Fatalf("query %q", seen.URL.RawQuery)
	}
}

func TestA401IsRetriedOnceWithAFreshToken(t *testing.T) {
	source := &tokens{}
	calls := 0
	c, _, _ := client(t, func(*http.Request) httpfake.Reply {
		calls++
		return httpfake.GraphError(401, "InvalidAuthenticationToken", "expired")
	}, Options{Token: source.source()})
	_, err := c.Get(context.Background(), "/v1.0/me", nil)
	if calls != 2 || source.refreshed != 1 {
		t.Fatalf("calls %d, refreshed %d", calls, source.refreshed)
	}
	found := errs.As(err)
	if found == nil || found.Status != 401 || found.Code != "InvalidAuthenticationToken" {
		t.Fatalf("error %v", err)
	}
	if !strings.Contains(found.Hint, "audience") {
		t.Fatalf("hint %q", found.Hint)
	}
}

func TestThrottlingWaitsAsRetryAfterSays(t *testing.T) {
	calls := 0
	c, _, slept := client(t, func(*http.Request) httpfake.Reply {
		calls++
		if calls < 3 {
			return httpfake.Reply{Status: 429, Headers: map[string]string{"Retry-After": "7"}}
		}
		return httpfake.JSON(map[string]any{"ok": true})
	}, Options{})
	if _, err := c.Get(context.Background(), "/x", nil); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 2 || (*slept)[0] != 7*time.Second {
		t.Fatalf("slept %v", *slept)
	}
}

func TestServerErrorsBackOffThenGiveUp(t *testing.T) {
	c, transport, slept := client(t, func(*http.Request) httpfake.Reply {
		return httpfake.GraphError(503, "ServiceUnavailable", "busy")
	}, Options{MaxAttempts: 3})
	_, err := c.Get(context.Background(), "/x", nil)
	if len(transport.Seen()) != 3 || len(*slept) != 2 {
		t.Fatalf("attempts %d, waits %v", len(transport.Seen()), *slept)
	}
	if (*slept)[1] < 2*time.Second {
		t.Fatalf("no backoff: %v", *slept)
	}
	if !errs.Is(err, errs.API) || !strings.Contains(err.Error(), "HTTP 503 ServiceUnavailable: busy") {
		t.Fatalf("error %v", err)
	}
}

func TestConnectionErrorsAreRetried(t *testing.T) {
	calls := 0
	c, _, _ := client(t, func(*http.Request) httpfake.Reply {
		calls++
		if calls == 1 {
			return httpfake.Reply{Err: &netError{}}
		}
		return httpfake.JSON(map[string]any{})
	}, Options{})
	if _, err := c.Get(context.Background(), "/x", nil); err != nil || calls != 2 {
		t.Fatalf("calls %d, err %v", calls, err)
	}
}

type netError struct{}

func (*netError) Error() string   { return "connection reset by peer" }
func (*netError) Timeout() bool   { return true }
func (*netError) Temporary() bool { return true }

func TestAllFollowsNextLinksOnTheSameHostAlone(t *testing.T) {
	c, _, _ := client(t, func(request *http.Request) httpfake.Reply {
		if request.URL.Query().Get("page") == "2" {
			return httpfake.JSON(map[string]any{"value": []any{map[string]any{"n": 3.0}}})
		}
		return httpfake.JSON(map[string]any{
			"value":           []any{map[string]any{"n": 1.0}, map[string]any{"n": 2.0}, "skipped"},
			"@odata.nextLink": "https://graph.example.test:443/v1.0/items?page=2",
		})
	}, Options{})
	items, err := Collect(c.All(context.Background(), "/v1.0/items", nil, ""))
	if err != nil || len(items) != 3 || items[2]["n"] != 3.0 {
		t.Fatalf("items %v, err %v", items, err)
	}
	if _, err := c.URL("https://evil.example.test/steal", nil); !errs.Is(err, errs.API) {
		t.Fatalf("a next link elsewhere was followed: %v", err)
	}
}

func TestErrorBodiesOfEveryShapeAreRead(t *testing.T) {
	cases := []struct {
		name  string
		reply httpfake.Reply
		want  string
	}{
		{"graph", httpfake.GraphError(403, "Authorization_RequestDenied", "Insufficient privileges"),
			"Graph: HTTP 403 Authorization_RequestDenied: Insufficient privileges"},
		{"entra", httpfake.Status(400, map[string]any{"error": "invalid_grant",
			"error_description": "AADSTS70000: bad grant\r\nTrace ID: 1"}), "HTTP 400 invalid_grant: AADSTS70000: bad grant"},
		{"jira", httpfake.Status(400, map[string]any{"errorMessages": []any{"Unbounded JQL"}}), "HTTP 400: Unbounded JQL"},
		{"confluence", httpfake.Status(404, map[string]any{"errors": []any{map[string]any{"title": "Not Found", "detail": "no page"}}}),
			"HTTP 404: Not Found: no page"},
		{"nested", httpfake.GraphError(400, "BadRequest", `{"ErrorCode":"Inner","Message":"{\"code\":\"Deeper\",\"message\":\"the reason\"}"}`),
			"HTTP 400 BadRequest: Inner: Deeper: the reason"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := client(t, func(*http.Request) httpfake.Reply { return tc.reply }, Options{MaxAttempts: 1})
			_, err := c.Get(context.Background(), "/x", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestHintsForKnownCodesAndSuspendedServices(t *testing.T) {
	c, _, _ := client(t, func(*http.Request) httpfake.Reply {
		return httpfake.GraphError(403, "TenantDisabled", "off")
	}, Options{MaxAttempts: 1, ErrorHints: map[string]string{"TenantDisabled": "needs a licence"}})
	_, err := c.Get(context.Background(), "/x", nil)
	if errs.HintOf(err) != "needs a licence" {
		t.Fatalf("hint %q", errs.HintOf(err))
	}
	c, _, _ = client(t, func(*http.Request) httpfake.Reply {
		return httpfake.GraphError(403, "Forbidden", "Account mode is Suspended")
	}, Options{MaxAttempts: 1})
	_, err = c.Get(context.Background(), "/x", nil)
	if !strings.Contains(errs.HintOf(err), "suspended") {
		t.Fatalf("hint %q", errs.HintOf(err))
	}
}

func TestOnlyHTTPSBaseURLsOrLocalHTTPWithoutAToken(t *testing.T) {
	if _, err := New(Options{BaseURL: "http://graph.example.test"}); err == nil {
		t.Fatal("plain http accepted")
	}
	if _, err := New(Options{BaseURL: "http://169.254.169.254", AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{BaseURL: "http://169.254.169.254", AllowHTTP: true, Token: &TokenSource{}}); err == nil {
		t.Fatal("a token would go over plain http")
	}
}

func TestRetryAfterReadsSecondsAndDates(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if RetryAfter("30", now) != 30*time.Second || RetryAfter("", now) != -1 || RetryAfter("soon", now) != -1 {
		t.Fatal("seconds")
	}
	if got := RetryAfter("Sun, 27 Sep 2026 12:01:00 GMT", now); got != time.Minute {
		t.Fatalf("date: %v", got)
	}
}

func TestAnEmptySuccessIsAnEmptyObjectWhenAllowed(t *testing.T) {
	c, _, _ := client(t, func(*http.Request) httpfake.Reply { return httpfake.Reply{Status: 204} }, Options{})
	got, err := c.Do(context.Background(), http.MethodPatch, "/x", Call{JSON: map[string]any{"a": 1}, AllowEmpty: true})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	_, err = c.Get(context.Background(), "/x", nil)
	if !errs.Is(err, errs.API) || !errors.Is(err, err) {
		t.Fatalf("a missing body was accepted: %v", err)
	}
}

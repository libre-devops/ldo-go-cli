// Package httpfake is a fake HTTP transport for tests: every request goes to a handler
// instead of the network, and is recorded. Nothing in a test touches the network.
package httpfake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Reply is what the fake answers a request with. Body is JSON-encoded unless it is
// []byte or Text; Err, when set, is a connection error instead.
type Reply struct {
	Status  int
	Body    any
	Headers map[string]string
	Err     error
}

// Text is a body sent as it is, with its own content type.
type Text struct {
	Content     string
	ContentType string
}

// JSON is a 200 reply with body as JSON.
func JSON(body any) Reply { return Reply{Status: 200, Body: body} }

// Status is a reply with status and body as JSON.
func Status(status int, body any) Reply { return Reply{Status: status, Body: body} }

// GraphError is Graph's error body, with an HTTP status.
func GraphError(status int, code, message string) Reply {
	return Reply{Status: status, Body: map[string]any{"error": map[string]any{"code": code, "message": message}}}
}

// Handler answers one request.
type Handler func(*http.Request) Reply

// Seen is one request as the fake received it, its body read.
type Seen struct {
	Method  string
	URL     *url.URL
	Header  http.Header
	Body    string
	Request *http.Request
}

// Path is the request's path, unescaped.
func (s Seen) Path() string { return s.URL.Path }

// Transport is an http.RoundTripper that routes requests to Handler and records them.
type Transport struct {
	Handler Handler
	mu      sync.Mutex
	seen    []Seen
}

// RoundTrip answers request with the handler's reply.
func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
		request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	t.mu.Lock()
	t.seen = append(t.seen, Seen{request.Method, request.URL, request.Header.Clone(), string(body), request})
	t.mu.Unlock()
	reply := t.Handler(request)
	if reply.Err != nil {
		return nil, reply.Err
	}
	return response(request, reply), nil
}

// Seen is every request the fake has received, in order.
func (t *Transport) Seen() []Seen {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Seen(nil), t.seen...)
}

// Paths is the method and path of every request, as "GET /v1.0/me".
func (t *Transport) Paths() []string {
	var found []string
	for _, seen := range t.Seen() {
		found = append(found, seen.Method+" "+seen.URL.Path)
	}
	return found
}

func response(request *http.Request, reply Reply) *http.Response {
	status := reply.Status
	if status == 0 {
		status = 200
	}
	contentType := "application/json"
	var content []byte
	switch body := reply.Body.(type) {
	case nil:
	case []byte:
		content = body
	case Text:
		content = []byte(body.Content)
		contentType = body.ContentType
		if contentType == "" {
			contentType = "text/plain"
		}
	default:
		content, _ = json.Marshal(body)
	}
	header := http.Header{"Content-Type": {contentType}}
	for key, value := range reply.Headers {
		header.Set(key, value)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(content)),
		ContentLength: int64(len(content)),
		Request:       request,
	}
}

// Client is an http.Client over a fake transport, and the transport for its log.
func Client(handler Handler) (*http.Client, *Transport) {
	transport := &Transport{Handler: handler}
	return &http.Client{Transport: transport}, transport
}

// Route is one answer: method and a path prefix ("GET /v1.0/users"), and the reply or a
// handler of its own.
type Route struct {
	Match string
	Reply Reply
	Func  Handler
}

// Routes is a handler answering each request from the first route whose method and path
// prefix match it, longest prefix first, and 404 for anything else, so an unexpected
// call fails its test loudly.
func Routes(routes ...Route) Handler {
	return func(request *http.Request) Reply {
		best := -1
		for index, route := range routes {
			method, prefix, _ := strings.Cut(route.Match, " ")
			if method != request.Method || !strings.HasPrefix(request.URL.Path, prefix) {
				continue
			}
			if best < 0 || len(prefix) > len(strings.SplitN(routes[best].Match, " ", 2)[1]) {
				best = index
			}
		}
		if best < 0 {
			return GraphError(404, "NotFound", "no fake route for "+request.Method+" "+request.URL.Path)
		}
		if routes[best].Func != nil {
			return routes[best].Func(request)
		}
		return routes[best].Reply
	}
}

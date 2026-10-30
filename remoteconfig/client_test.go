package remoteconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTokens is a TokenSource that hands out numbered tokens and counts
// invalidations, so a test can tell a retry used a fresh credential.
type fakeTokens struct {
	mu          sync.Mutex
	issued      int
	invalidated int
	err         error
}

func (f *fakeTokens) Token(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.issued++
	return "tok" + string(rune('0'+f.issued)), nil
}

func (f *fakeTokens) Invalidate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated++
}

// server answers each request with the next response in the list and records
// what it was asked.
type server struct {
	t         *testing.T
	responses []response
	mu        sync.Mutex
	requests  []*http.Request
}

type response struct {
	status  int
	body    string
	headers map[string]string
}

func newServer(t *testing.T, responses ...response) (*server, *httptest.Server) {
	t.Helper()
	s := &server{t: t, responses: responses}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, r)
		s.mu.Unlock()
		if n >= len(s.responses) {
			t.Errorf("unexpected request %d to %s", n+1, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		resp := s.responses[n]
		for k, v := range resp.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(ts.Close)
	return s, ts
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *server) request(i int) *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[i]
}

func ok(body string) response { return response{status: http.StatusOK, body: body} }

func TestGetDecodesDocument(t *testing.T) {
	_, ts := newServer(t, ok(`{"a":1,"b":"two"}`))
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	if err := c.Get(context.Background(), "sites", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc.A != 1 || doc.B != "two" {
		t.Fatalf("decoded %+v", doc)
	}
}

func TestGetRequestShape(t *testing.T) {
	s, ts := newServer(t, ok(`{}`))
	c := &Client{BaseURL: ts.URL + "/", Tokens: &fakeTokens{}, UserAgent: "greenhouse/abc123"}

	var doc map[string]any
	if err := c.Get(context.Background(), "devices_home", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
	r := s.request(0)
	if r.Method != http.MethodGet {
		t.Errorf("method = %s", r.Method)
	}
	// The trailing slash on BaseURL must not produce a double slash.
	if r.URL.Path != "/api/v1/config/devices_home" {
		t.Errorf("path = %q", r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer tok1" {
		t.Errorf("Authorization = %q", got)
	}
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if got := r.Header.Get("User-Agent"); got != "greenhouse/abc123" {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestGetDefaultUserAgent(t *testing.T) {
	s, ts := newServer(t, ok(`{}`))
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}}

	var doc map[string]any
	if err := c.Get(context.Background(), "sites", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := s.request(0).Header.Get("User-Agent"); got != DefaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", got, DefaultUserAgent)
	}
}

func TestGetAnonymousWithoutTokenSource(t *testing.T) {
	s, ts := newServer(t, ok(`{"public":true}`))
	c := &Client{BaseURL: ts.URL, UserAgent: "test/1"}

	var doc map[string]any
	if err := c.Get(context.Background(), "energy_tariff_screen", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := s.request(0).Header.Get("Authorization"); got != "" {
		t.Errorf("anonymous request sent Authorization %q", got)
	}
}

func TestGetAnonymous401IsNotRetried(t *testing.T) {
	s, ts := newServer(t, response{status: http.StatusUnauthorized})
	c := &Client{BaseURL: ts.URL, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if s.count() != 1 {
		t.Fatalf("requests = %d, want 1", s.count())
	}
}

func TestGet401RetriesOnceWithFreshToken(t *testing.T) {
	s, ts := newServer(t, response{status: http.StatusUnauthorized}, ok(`{"x":1}`))
	tokens := &fakeTokens{}
	c := &Client{BaseURL: ts.URL, Tokens: tokens, UserAgent: "test/1"}

	var doc map[string]int
	if err := c.Get(context.Background(), "sites", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc["x"] != 1 {
		t.Fatalf("decoded %v", doc)
	}
	if tokens.invalidated != 1 {
		t.Errorf("invalidated = %d, want 1", tokens.invalidated)
	}
	if s.count() != 2 {
		t.Fatalf("requests = %d, want 2", s.count())
	}
	if got := s.request(1).Header.Get("Authorization"); got != "Bearer tok2" {
		t.Errorf("retry Authorization = %q, want the fresh token", got)
	}
}

func TestGet401TwiceGivesUp(t *testing.T) {
	s, ts := newServer(t,
		response{status: http.StatusUnauthorized},
		response{status: http.StatusUnauthorized})
	tokens := &fakeTokens{}
	c := &Client{BaseURL: ts.URL, Tokens: tokens, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if s.count() != 2 {
		t.Errorf("requests = %d, want 2 (one retry, no more)", s.count())
	}
	// Both rejected tokens are dropped, so the next Get starts fresh too.
	if tokens.invalidated != 2 {
		t.Errorf("invalidated = %d, want 2", tokens.invalidated)
	}
}

func TestGetStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		resp       response
		want       error
		code       int
		retryAfter time.Duration
	}{
		{"not found", response{status: 404, body: `{"error":"not_found","message":"namespace not found"}`}, ErrNotFound, 404, 0},
		{"forbidden", response{status: 403, body: `{"error":"forbidden","message":"unrecognised role in token"}`}, ErrForbidden, 403, 0},
		{"identity down", response{status: 503, headers: map[string]string{"Retry-After": "30"}}, ErrUnavailable, 503, 30 * time.Second},
		{"rate limited", response{status: 429, headers: map[string]string{"Retry-After": "1"}}, ErrUnavailable, 429, time.Second},
		{"unparseable retry-after", response{status: 503, headers: map[string]string{"Retry-After": "soon"}}, ErrUnavailable, 503, 0},
		{"other", response{status: 500, body: "boom"}, ErrUnexpectedStatus, 500, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newServer(t, tc.resp)
			tokens := &fakeTokens{}
			c := &Client{BaseURL: ts.URL, Tokens: tokens, UserAgent: "test/1"}

			var doc map[string]any
			err := c.Get(context.Background(), "sites", &doc)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("err %v is not a *StatusError", err)
			}
			if se.Namespace != "sites" || se.Code != tc.code || se.RetryAfter != tc.retryAfter {
				t.Errorf("StatusError = %+v", se)
			}
			if !strings.Contains(err.Error(), `"sites"`) {
				t.Errorf("error %q does not name the namespace", err)
			}
			// Only a 401 says the token is at fault. A 503 in particular is
			// config failing to reach identity, and dropping a good token then
			// would only add load to the outage.
			if tokens.invalidated != 0 {
				t.Errorf("invalidated = %d on %d, want 0", tokens.invalidated, tc.code)
			}
		})
	}
}

func TestGetErrorCarriesServerMessage(t *testing.T) {
	_, ts := newServer(t, response{status: 403, body: `{"error":"forbidden","message":"unrecognised role in token"}`})
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	if !strings.Contains(err.Error(), "unrecognised role in token") {
		t.Errorf("error %q drops the server's message", err)
	}
}

func TestGetNotFoundSaysEitherMissingOrUnreadable(t *testing.T) {
	_, ts := newServer(t, response{status: 404})
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	// config answers 404 for a namespace this caller may not read as well as
	// for one that does not exist, deliberately. The message must not claim
	// to know which.
	if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("error %q claims more than a 404 says", err)
	}
}

func TestGetRejectsBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		ns      string
		want    error
	}{
		{"no base url", "", "sites", ErrNotConfigured},
		{"empty name", "BASE", "", ErrInvalidName},
		{"uppercase", "BASE", "Sites", ErrInvalidName},
		{"slash", "BASE", "a/b", ErrInvalidName},
		{"dot dot", "BASE", "..", ErrInvalidName},
		{"too long", "BASE", strings.Repeat("a", 65), ErrInvalidName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ts := newServer(t)
			base := tc.baseURL
			if base == "BASE" {
				base = ts.URL
			}
			tokens := &fakeTokens{}
			c := &Client{BaseURL: base, Tokens: tokens, UserAgent: "test/1"}

			var doc map[string]any
			err := c.Get(context.Background(), tc.ns, &doc)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if s.count() != 0 {
				t.Errorf("requests = %d, want 0", s.count())
			}
			if tokens.issued != 0 {
				t.Errorf("tokens issued = %d, want 0", tokens.issued)
			}
		})
	}
}

func TestGetLongestValidName(t *testing.T) {
	_, ts := newServer(t, ok(`{}`))
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]any
	if err := c.Get(context.Background(), strings.Repeat("a", 64), &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestGetTokenFailureNamesNamespace(t *testing.T) {
	s, ts := newServer(t)
	cause := errors.New("identity unreachable")
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{err: cause}, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "devices_home", &doc)
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want it to wrap the token error", err)
	}
	if !strings.Contains(err.Error(), `"devices_home"`) {
		t.Errorf("error %q does not name the namespace", err)
	}
	if s.count() != 0 {
		t.Errorf("requests = %d, want 0", s.count())
	}
}

func TestGetTokenFailureOnRetry(t *testing.T) {
	_, ts := newServer(t, response{status: http.StatusUnauthorized})
	tokens := &failSecond{}
	c := &Client{BaseURL: ts.URL, Tokens: tokens, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	if !errors.Is(err, errSecond) {
		t.Fatalf("err = %v, want the retry's token error", err)
	}
}

var errSecond = errors.New("second token refused")

// failSecond issues one token and then fails, which is identity going away
// between a 401 and the retry.
type failSecond struct{ n int }

func (f *failSecond) Token(context.Context) (string, error) {
	f.n++
	if f.n > 1 {
		return "", errSecond
	}
	return "tok", nil
}
func (f *failSecond) Invalidate() {}

func TestGetBodies(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"null", `null`},
		{"array", `[1,2]`},
		{"string", `"x"`},
		{"empty", ``},
		{"invalid json", `{"a":`},
		{"oversize", `{"a":"` + strings.Repeat("x", maxDocumentBytes) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newServer(t, ok(tc.body))
			c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

			var doc map[string]any
			err := c.Get(context.Background(), "sites", &doc)
			if !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("err = %v, want ErrInvalidDocument", err)
			}
		})
	}
}

func TestGetAcceptsObjectWithLeadingWhitespace(t *testing.T) {
	_, ts := newServer(t, ok(" \n\t{\"a\":1}"))
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]int
	if err := c.Get(context.Background(), "sites", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestGetDocumentAtSizeLimit(t *testing.T) {
	pad := maxDocumentBytes - len(`{"a":""}`)
	_, ts := newServer(t, ok(`{"a":"`+strings.Repeat("x", pad)+`"}`))
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]string
	if err := c.Get(context.Background(), "sites", &doc); err != nil {
		t.Fatalf("Get at exactly the limit: %v", err)
	}
}

func TestGetRawMessage(t *testing.T) {
	_, ts := newServer(t, ok(`{"headline":{"rising_by_p":5}}`))
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var raw json.RawMessage
	if err := c.Get(context.Background(), "energy_tariff_screen", &raw); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(raw) != `{"headline":{"rising_by_p":5}}` {
		t.Errorf("raw = %s", raw)
	}
}

func TestGetTransportError(t *testing.T) {
	_, ts := newServer(t)
	url := ts.URL
	ts.Close()
	c := &Client{BaseURL: url, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	if err == nil {
		t.Fatal("Get against a closed server succeeded")
	}
	if !strings.Contains(err.Error(), `"sites"`) {
		t.Errorf("error %q does not name the namespace", err)
	}
}

func TestGetBadBaseURL(t *testing.T) {
	c := &Client{BaseURL: "http://bad host", Tokens: &fakeTokens{}, UserAgent: "test/1"}

	var doc map[string]any
	if err := c.Get(context.Background(), "sites", &doc); err == nil {
		t.Fatal("Get with an unparseable base URL succeeded")
	}
}

func TestGetHonoursContext(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(block) })
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1"}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var doc map[string]any
	err := c.Get(ctx, "sites", &doc)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestGetUsesSuppliedHTTPClient(t *testing.T) {
	_, ts := newServer(t, ok(`{}`))
	rt := &countingTransport{next: http.DefaultTransport}
	c := &Client{BaseURL: ts.URL, Tokens: &fakeTokens{}, UserAgent: "test/1",
		HTTPClient: &http.Client{Transport: rt}}

	var doc map[string]any
	if err := c.Get(context.Background(), "sites", &doc); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rt.n != 1 {
		t.Errorf("supplied client used %d times, want 1", rt.n)
	}
}

type countingTransport struct {
	next http.RoundTripper
	n    int
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return c.next.RoundTrip(r)
}

func TestGetBodyReadError(t *testing.T) {
	c := &Client{BaseURL: "http://config.test", Tokens: &fakeTokens{}, UserAgent: "test/1",
		HTTPClient: &http.Client{Transport: brokenBody{}}}

	var doc map[string]any
	err := c.Get(context.Background(), "sites", &doc)
	if !errors.Is(err, errBrokenBody) {
		t.Fatalf("err = %v, want the body read error", err)
	}
}

var errBrokenBody = errors.New("connection reset mid-body")

type brokenBody struct{}

func (brokenBody) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       errReader{},
		Request:    r,
	}, nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBrokenBody }
func (errReader) Close() error             { return nil }

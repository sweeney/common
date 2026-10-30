// Package remoteconfig reads namespaces from the config service
// (config.swee.net).
//
// It is the transport and the bookkeeping only: [Client.Get] fetches one
// namespace and decodes it, and [Tracker] records per-namespace outcomes for
// /healthz and for a cold-start check. What a service does with a document,
// and how it holds the last good one, stays in that service.
//
// The client is written against what the config service actually promises:
//
//   - 200: the body is the document, and is always a JSON object.
//   - 404: the namespace is missing OR this caller may not read it. config
//     answers both the same way, deliberately, so [ErrNotFound] says both.
//   - 401: the token is bad or expired. It is invalidated and the request
//     retried once with a fresh one.
//   - 403: the token verified but carries a role config does not recognise.
//   - 503: config cannot reach identity's JWKS. The token is not at fault and
//     is kept. 429 is the rate limit. Both are [ErrUnavailable], with
//     Retry-After.
//
// There is no ETag or version to make a conditional request with; every read
// is a full read.
package remoteconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TokenSource supplies bearer tokens for the config service. It is satisfied
// by identity/common/auth.TokenSource, without this module depending on it.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	// Invalidate drops any cached token. Get calls it on a 401 so the retry,
	// and every later call, gets a fresh one rather than replaying the
	// rejected credential for its full TTL.
	Invalidate()
}

// DefaultUserAgent is sent when Client.UserAgent is empty.
const DefaultUserAgent = "sweeney-remoteconfig"

// maxDocumentBytes caps a response body. config refuses to store a document
// over 128 KiB, so this is headroom rather than a limit anyone should meet.
const maxDocumentBytes = 1 << 20

// defaultHTTPClient is used when Client.HTTPClient is nil.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

// nameRE is config's own rule for a namespace name. Checking it here means a
// mistake such as an unset name is reported as itself, rather than as
// whatever /api/v1/config/ happens to answer.
var nameRE = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// Errors returned by Get. Those from a response arrive wrapped in a
// *StatusError; test for them with errors.Is.
var (
	// ErrNotConfigured: Client.BaseURL is empty. That is the explicit local
	// mode, so nothing is requested and no token is fetched.
	ErrNotConfigured = errors.New("no config service configured")
	// ErrInvalidName: the namespace name fails ^[a-z0-9_-]{1,64}$. Nothing is
	// requested.
	ErrInvalidName = errors.New("invalid namespace name")
	// ErrNotFound: 404. The namespace does not exist, or this caller may not
	// read it; config does not say which.
	ErrNotFound = errors.New("namespace not found, or not readable by this caller")
	// ErrUnauthorized: 401, after one retry with a fresh token (or with no
	// retry, for a Client with no TokenSource).
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden: 403. Retrying will not help.
	ErrForbidden = errors.New("forbidden")
	// ErrUnavailable: 503 (config cannot verify tokens because identity is
	// unreachable) or 429 (rate limited). Keep the last good document.
	ErrUnavailable = errors.New("config service unavailable")
	// ErrUnexpectedStatus: any other non-200 answer.
	ErrUnexpectedStatus = errors.New("unexpected status")
	// ErrInvalidDocument: a 200 whose body is not a JSON object that decodes
	// into the destination, or is over the size cap.
	ErrInvalidDocument = errors.New("invalid document")
)

// StatusError is a non-200 answer from the config service.
type StatusError struct {
	Namespace string
	Code      int
	// RetryAfter is the server's Retry-After, in whole seconds, when it sent
	// one. Zero otherwise.
	RetryAfter time.Duration
	// Message is the "message" field of config's JSON error body, if any.
	Message string
	// Err is the sentinel the code maps to.
	Err error
}

func (e *StatusError) Error() string {
	s := fmt.Sprintf("remoteconfig: namespace %q: %v (%d)", e.Namespace, e.Err, e.Code)
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

func (e *StatusError) Unwrap() error { return e.Err }

// Client reads namespaces from the config service. The zero value is local
// mode: every Get returns ErrNotConfigured.
type Client struct {
	// BaseURL is the service root, e.g. https://config.swee.net. A trailing
	// slash is ignored. Empty means local mode.
	BaseURL string
	// Tokens supplies bearer tokens. Nil sends anonymous requests, which can
	// read only namespaces published with read_role public.
	Tokens TokenSource
	// UserAgent identifies the caller, conventionally "<service>/<version>",
	// so config's logs say who is reading. Empty sends DefaultUserAgent.
	UserAgent string
	// HTTPClient is used for requests. Nil uses a client with a 10 s timeout.
	HTTPClient *http.Client
}

// Get fetches namespace ns and decodes it into dst with encoding/json, which
// ignores keys dst does not declare.
//
// It does not record anything: a caller that validates the document further
// should record the combined outcome with Tracker.Record, so that a document
// which fetched but failed validation never counts as landed.
func (c *Client) Get(ctx context.Context, ns string, dst any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("remoteconfig: namespace %q: %w", ns, ErrNotConfigured)
	}
	if !nameRE.MatchString(ns) {
		return fmt.Errorf("remoteconfig: namespace %q: %w", ns, ErrInvalidName)
	}

	body, err := c.do(ctx, ns)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusUnauthorized && c.Tokens != nil {
		// A token that expired between being minted and being used is
		// expected, not an error: drop it and try once more.
		c.Tokens.Invalidate()
		body, err = c.do(ctx, ns)
		if errors.As(err, &se) && se.Code == http.StatusUnauthorized {
			c.Tokens.Invalidate()
		}
	}
	if err != nil {
		return err
	}

	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		// config stores only objects. Anything else, null above all, must not
		// count as a successful fetch of an empty document.
		return fmt.Errorf("remoteconfig: namespace %q: %w: not a JSON object", ns, ErrInvalidDocument)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("remoteconfig: namespace %q: %w: %v", ns, ErrInvalidDocument, err)
	}
	return nil
}

// do performs one GET, returning the body of a 200 or a *StatusError.
func (c *Client) do(ctx context.Context, ns string) ([]byte, error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/api/v1/config/" + ns
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("remoteconfig: namespace %q: build request: %w", ns, err)
	}
	if c.Tokens != nil {
		tok, err := c.Tokens.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("remoteconfig: namespace %q: token: %w", ns, err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Accept", "application/json")
	ua := c.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	hc := c.HTTPClient
	if hc == nil {
		hc = defaultHTTPClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remoteconfig: namespace %q: %w", ns, err)
	}
	defer resp.Body.Close()

	// One byte over the cap, so an oversized answer is detected rather than
	// silently truncated into invalid JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("remoteconfig: namespace %q: read body: %w", ns, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(ns, resp, body)
	}
	if len(body) > maxDocumentBytes {
		return nil, fmt.Errorf("remoteconfig: namespace %q: %w: over %d bytes", ns, ErrInvalidDocument, maxDocumentBytes)
	}
	return body, nil
}

func statusError(ns string, resp *http.Response, body []byte) *StatusError {
	se := &StatusError{Namespace: ns, Code: resp.StatusCode}
	switch resp.StatusCode {
	case http.StatusNotFound:
		se.Err = ErrNotFound
	case http.StatusUnauthorized:
		se.Err = ErrUnauthorized
	case http.StatusForbidden:
		se.Err = ErrForbidden
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		se.Err = ErrUnavailable
	default:
		se.Err = ErrUnexpectedStatus
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		se.RetryAfter = time.Duration(secs) * time.Second
	}
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		se.Message = envelope.Message
	}
	return se
}

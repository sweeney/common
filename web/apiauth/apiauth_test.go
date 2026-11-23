package apiauth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	errKeys    = errors.New("keys unavailable")
	errExpired = errors.New("token expired")
)

type user struct{ active bool }
type service struct{ client string }

// jwt is a token with the JOSE header {"typ": typ} and payload body, as
// far as the middleware looks (it reads only the header's typ).
func jwt(typ, body string) string {
	h := `{"alg":"ES256"}`
	if typ != "" {
		h = `{"alg":"ES256","typ":"` + typ + `"}`
	}
	e := base64.RawURLEncoding.EncodeToString
	return e([]byte(h)) + "." + e([]byte(body)) + ".sig"
}

// fakeVerifier knows a token by its payload: "u:<active>" for a user,
// "s:<client>" for a service, "expired"; anything else is invalid. keys
// makes every check fail as identity's does when it can't get the keys.
// Each call is counted: each is a signature check.
type fakeVerifier struct {
	keys  bool
	mu    sync.Mutex
	calls int
}

func (f *fakeVerifier) Issuer() string { return "https://id.example/\"x\"\n\\\x7f" }

func (f *fakeVerifier) payload(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || strings.ContainsAny(tok, " \t") { // a JWT has no whitespace
		return ""
	}
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	return string(b)
}

func (f *fakeVerifier) count() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.keys {
		return errKeys
	}
	return nil
}

func (f *fakeVerifier) Parse(_ context.Context, tok string) (user, error) {
	if err := f.count(); err != nil {
		return user{}, err
	}
	p := f.payload(tok)
	if p == "expired" {
		return user{}, errExpired
	}
	if a, ok := strings.CutPrefix(p, "u:"); ok {
		return user{active: a == "true"}, nil
	}
	return user{}, errors.New("invalid")
}

func (f *fakeVerifier) ParseServiceToken(_ context.Context, tok string) (service, error) {
	if err := f.count(); err != nil {
		return service{}, err
	}
	p := f.payload(tok)
	if p == "expired" {
		return service{}, errExpired
	}
	if c, ok := strings.CutPrefix(p, "s:"); ok {
		return service{client: c}, nil
	}
	return service{}, errors.New("invalid")
}

func config(v *fakeVerifier) Config[user, service] {
	return Config[user, service]{
		Verifier: v,
		UserOK: func(u user) error {
			if !u.active {
				return &Refusal{Status: http.StatusUnauthorized, Code: "invalid_token", Description: "user account is not active"}
			}
			return nil
		},
		ServiceOK: func(s service) error {
			switch s.client {
			case "stranger":
				return errors.New("not on the list") // a plain error: 403
			case "scopeless":
				return &Refusal{Status: http.StatusForbidden, Code: "insufficient_scope",
					Description: "token does not carry the scope this API requires", Scope: "washhouse:read"}
			case "elsewhere":
				return &Refusal{Status: http.StatusUnauthorized, Code: "invalid_token", Description: "token audience does not include this server"}
			}
			return nil
		},
		KeysUnavailable: errKeys,
		TokenExpired:    errExpired,
	}
}

func newAuth(t *testing.T, c Config[user, service]) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return mw
}

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func do(h http.Handler, authz string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The realm is the issuer with the characters that could restructure the
// challenge (quotes, backslashes, controls) dropped.
const realm = `Bearer realm="https://id.example/x"`

func TestRequire(t *testing.T) {
	invalid := realm + `, error="invalid_token", error_description="token is malformed, or signed by a key this server does not trust"`
	for _, tc := range []struct {
		name      string
		keys      bool
		authz     string
		code      int
		body      string
		challenge string // "" for none
		outcome   Outcome
	}{
		{"no header", false, "", 401, `{"error":"unauthorized"}`, realm, Missing},
		{"not bearer", false, "Basic dTpw", 401, `{"error":"unauthorized"}`, realm, Missing},
		{"empty bearer", false, "Bearer ", 401, `{"error":"unauthorized"}`, realm, Missing},
		{"bearer run together", false, "Bearer" + jwt("", "u:true"), 401, `{"error":"unauthorized"}`, realm, Missing},
		{"active user", false, "Bearer " + jwt("", "u:true"), 418, "", "", UserAccepted},
		{"lower-case scheme, tab", false, "bearer\t" + jwt("", "u:true"), 418, "", "", UserAccepted},
		{"spaces round the token", false, "BEARER   " + jwt("", "u:true") + "  ", 418, "", "", UserAccepted},
		{"inactive user", false, "Bearer " + jwt("", "u:false"), 401, `{"error":"unauthorized"}`,
			realm + `, error="invalid_token", error_description="user account is not active"`, UserRefused},
		{"service", false, "Bearer " + jwt("at+jwt", "s:statehouse"), 418, "", "", ServiceAccepted},
		{"service not on the list", false, "Bearer " + jwt("at+jwt", "s:stranger"), 403, `{"error":"forbidden"}`,
			realm + `, error="insufficient_scope", error_description="client is not permitted to call this API"`, ServiceRefused},
		{"service without the scope", false, "Bearer " + jwt("at+jwt", "s:scopeless"), 403, `{"error":"forbidden"}`,
			realm + `, error="insufficient_scope", error_description="token does not carry the scope this API requires", scope="washhouse:read"`, ServiceRefused},
		{"service for another audience", false, "Bearer " + jwt("at+jwt", "s:elsewhere"), 401, `{"error":"unauthorized"}`,
			realm + `, error="invalid_token", error_description="token audience does not include this server"`, ServiceRefused},
		{"a user token marked as a service's", false, "Bearer " + jwt("at+jwt", "u:true"), 401, `{"error":"unauthorized"}`, invalid, Invalid},
		{"not a JWT", false, "Bearer junk", 401, `{"error":"unauthorized"}`, invalid, Invalid},
		{"expired user", false, "Bearer " + jwt("", "expired"), 401, `{"error":"unauthorized"}`,
			realm + `, error="invalid_token", error_description="token expired"`, Expired},
		{"expired service", false, "Bearer " + jwt("at+jwt", "expired"), 401, `{"error":"unauthorized"}`,
			realm + `, error="invalid_token", error_description="token expired"`, Expired},
		{"keys unavailable, user", true, "Bearer " + jwt("", "u:true"), 503, `{"error":"authentication temporarily unavailable"}`, "", Unavailable},
		{"keys unavailable, service", true, "Bearer " + jwt("at+jwt", "s:x"), 503, `{"error":"authentication temporarily unavailable"}`, "", Unavailable},
		// No token is no credential, even in an outage: never sent to be checked.
		{"empty bearer, keys unavailable", true, "Bearer ", 401, `{"error":"unauthorized"}`, realm, Missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &fakeVerifier{keys: tc.keys}
			c := config(v)
			var got []Result[user, service]
			c.OnResult = func(_ *http.Request, r Result[user, service]) { got = append(got, r) }
			w := do(newAuth(t, c)(ok), tc.authz)
			if w.Code != tc.code || strings.TrimSpace(w.Body.String()) != tc.body {
				t.Errorf("%d %q, want %d %q", w.Code, w.Body.String(), tc.code, tc.body)
			}
			if ch := w.Header().Get("WWW-Authenticate"); ch != tc.challenge {
				t.Errorf("challenge %q\nwant      %q", ch, tc.challenge)
			}
			if wantRetry := tc.code == 503; wantRetry != (w.Header().Get("Retry-After") == "30") {
				t.Errorf("Retry-After %q", w.Header().Get("Retry-After"))
			}
			if len(got) != 1 || got[0].Outcome != tc.outcome {
				t.Errorf("results %+v, want one %v", got, tc.outcome)
			}
			// One signature check per token, none without one.
			want := 1
			if tc.outcome == Missing {
				want = 0
			}
			if v.calls != want {
				t.Errorf("%d checks, want %d", v.calls, want)
			}
		})
	}
}

// The result carries the claims, and the refusal or the verifier's error.
func TestResultCarriesTheClaims(t *testing.T) {
	c := config(&fakeVerifier{})
	var got Result[user, service]
	c.OnResult = func(_ *http.Request, r Result[user, service]) { got = r }
	h := newAuth(t, c)(ok)
	do(h, "Bearer "+jwt("at+jwt", "s:scopeless"))
	if got.Service.client != "scopeless" || got.Refusal == nil || got.Refusal.Scope != "washhouse:read" {
		t.Errorf("%+v", got)
	}
	do(h, "Bearer "+jwt("", "expired"))
	if !errors.Is(got.Err, errExpired) {
		t.Errorf("%+v", got)
	}
	do(h, "Bearer "+jwt("", "u:true"))
	if !got.User.active || got.Err != nil || got.Refusal != nil {
		t.Errorf("%+v", got)
	}
}

// The handler can read who called.
func TestClaimsInTheContext(t *testing.T) {
	var u user
	var uok, sok bool
	h := newAuth(t, config(&fakeVerifier{}))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		u, uok = UserFrom[user](r.Context())
		_, sok = ServiceFrom[service](r.Context())
	}))
	do(h, "Bearer "+jwt("", "u:true"))
	if !uok || !u.active || sok {
		t.Errorf("user %v %v, service %v", u, uok, sok)
	}
	var s service
	h = newAuth(t, config(&fakeVerifier{}))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		s, sok = ServiceFrom[service](r.Context())
		_, uok = UserFrom[user](r.Context())
	}))
	do(h, "Bearer "+jwt("at+jwt", "s:statehouse"))
	if !sok || s.client != "statehouse" || uok {
		t.Errorf("service %v %v, user %v", s, sok, uok)
	}
}

// A Refusal with a status other than 401 or 403 is a 403: a policy can't
// make a refusal look like success or an outage. A plain error from UserOK
// is a 401 invalid_token, its text never sent.
func TestRefusalStatusIs401Or403(t *testing.T) {
	c := config(&fakeVerifier{})
	c.ServiceOK = func(service) error { return &Refusal{Status: http.StatusOK, Code: "insufficient_scope"} }
	if w := do(newAuth(t, c)(ok), "Bearer "+jwt("at+jwt", "s:x")); w.Code != http.StatusForbidden {
		t.Errorf("%d", w.Code)
	}
	c.UserOK = func(user) error { return errors.New("secret reason") }
	w := do(newAuth(t, c)(ok), "Bearer "+jwt("", "u:true"))
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != realm+`, error="invalid_token", error_description="user is not permitted"` {
		t.Errorf("a plain error from UserOK: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
}

func TestServiceTypIsConfigurable(t *testing.T) {
	c := config(&fakeVerifier{})
	c.ServiceTyp = "svc+jwt"
	h := newAuth(t, c)(ok)
	if w := do(h, "Bearer "+jwt("svc+jwt", "s:statehouse")); w.Code != http.StatusTeapot {
		t.Errorf("svc+jwt: %d", w.Code)
	}
	if w := do(h, "Bearer "+jwt("at+jwt", "s:statehouse")); w.Code != http.StatusUnauthorized {
		t.Errorf("at+jwt: %d", w.Code)
	}
}

func TestRetryAfterIsConfigurable(t *testing.T) {
	c := config(&fakeVerifier{keys: true})
	c.RetryAfter = 1500 * time.Millisecond
	if w := do(newAuth(t, c)(ok), "Bearer "+jwt("", "u:true")); w.Header().Get("Retry-After") != "2" { // rounded up
		t.Errorf("Retry-After %q", w.Header().Get("Retry-After"))
	}
}

func TestAnyService(t *testing.T) {
	if AnyService(service{client: "stranger"}) != nil {
		t.Error("refused")
	}
}

func TestRefusalIsAnError(t *testing.T) {
	var err error = &Refusal{Status: 403, Code: "insufficient_scope", Description: "no"}
	var r *Refusal
	if !errors.As(err, &r) || err.Error() != "403 insufficient_scope: no" {
		t.Errorf("%v", err)
	}
}

// Every policy is explicit: a nil one would quietly admit everyone, or
// sign everyone out in an identity outage.
func TestNewRefusesAMissingPart(t *testing.T) {
	for name, edit := range map[string]func(*Config[user, service]){
		"Verifier":        func(c *Config[user, service]) { c.Verifier = nil },
		"UserOK":          func(c *Config[user, service]) { c.UserOK = nil },
		"ServiceOK":       func(c *Config[user, service]) { c.ServiceOK = nil },
		"KeysUnavailable": func(c *Config[user, service]) { c.KeysUnavailable = nil },
		"RetryAfter":      func(c *Config[user, service]) { c.RetryAfter = -time.Second },
	} {
		c := config(&fakeVerifier{})
		edit(&c)
		if _, err := New(c); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	c := config(&fakeVerifier{})
	c.TokenExpired = nil // optional: expiry is then just invalid
	if _, err := New(c); err != nil {
		t.Error(err)
	}
}

func TestOutcomeNames(t *testing.T) {
	for o, want := range map[Outcome]string{Missing: "missing", Invalid: "invalid", Expired: "expired",
		UserRefused: "user refused", ServiceRefused: "service refused", Unavailable: "unavailable",
		UserAccepted: "user accepted", ServiceAccepted: "service accepted", Outcome(99): "Outcome(99)"} {
		if o.String() != want {
			t.Errorf("%d: %q, want %q", int(o), o.String(), want)
		}
	}
}

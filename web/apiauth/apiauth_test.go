package apiauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var errKeys = errors.New("keys unavailable")

type user struct{ active bool }
type service struct{ client string }

// fakeVerifier knows tokens by name: "u:<active>", "s:<client>"; "keys"
// can't be checked; anything else is invalid. Each parser refuses the
// other kind's token, as identity's does.
type fakeVerifier struct{ userKeys, serviceKeys bool }

func (f fakeVerifier) Issuer() string { return `https://id.example/"x"` }

func (f fakeVerifier) Parse(_ context.Context, tok string) (user, error) {
	if f.userKeys {
		return user{}, errKeys
	}
	if a, ok := strings.CutPrefix(tok, "u:"); ok {
		return user{active: a == "true"}, nil
	}
	return user{}, errors.New("not a user token")
}

func (f fakeVerifier) ParseServiceToken(_ context.Context, tok string) (service, error) {
	if f.serviceKeys {
		return service{}, errKeys
	}
	if c, ok := strings.CutPrefix(tok, "s:"); ok {
		return service{client: c}, nil
	}
	return service{}, errors.New("not a service token")
}

func newAuth(t *testing.T, v fakeVerifier) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := New(Config[user, service]{
		Verifier:        v,
		UserOK:          func(u user) bool { return u.active },
		ServiceOK:       func(s service) bool { return s.client != "stranger" },
		KeysUnavailable: errKeys,
	})
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

func TestRequire(t *testing.T) {
	for _, tc := range []struct {
		name   string
		v      fakeVerifier
		authz  string
		code   int
		body   string
		challe bool
	}{
		{"no header", fakeVerifier{}, "", 401, `{"error":"unauthorized"}`, true},
		{"not bearer", fakeVerifier{}, "Basic dTpw", 401, `{"error":"unauthorized"}`, true},
		{"empty bearer", fakeVerifier{}, "Bearer ", 401, `{"error":"unauthorized"}`, true},
		// No token is no credential, even in an outage: never sent to be checked.
		{"empty bearer, keys unavailable", fakeVerifier{userKeys: true, serviceKeys: true}, "Bearer ", 401, `{"error":"unauthorized"}`, true},
		{"active user", fakeVerifier{}, "Bearer u:true", 418, "", false},
		{"inactive user", fakeVerifier{}, "Bearer u:false", 401, `{"error":"unauthorized"}`, true},
		{"service", fakeVerifier{}, "Bearer s:statehouse", 418, "", false},
		{"service refused", fakeVerifier{}, "Bearer s:stranger", 403, `{"error":"forbidden"}`, false},
		{"invalid", fakeVerifier{}, "Bearer junk", 401, `{"error":"unauthorized"}`, true},
		{"keys unavailable to the user parse", fakeVerifier{userKeys: true}, "Bearer s:x", 418, "", false},
		{"keys unavailable to both", fakeVerifier{userKeys: true, serviceKeys: true}, "Bearer s:x", 503,
			`{"error":"authentication temporarily unavailable"}`, false},
		{"keys unavailable to the user parse, invalid token", fakeVerifier{userKeys: true}, "Bearer junk", 503,
			`{"error":"authentication temporarily unavailable"}`, false},
		{"keys unavailable to the service parse, invalid token", fakeVerifier{serviceKeys: true}, "Bearer junk", 503,
			`{"error":"authentication temporarily unavailable"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := do(newAuth(t, tc.v)(ok), tc.authz)
			if w.Code != tc.code || strings.TrimSpace(w.Body.String()) != tc.body {
				t.Errorf("%d %q, want %d %q", w.Code, w.Body.String(), tc.code, tc.body)
			}
			ch := w.Header().Get("WWW-Authenticate")
			if tc.challe != (ch != "") {
				t.Errorf("WWW-Authenticate %q", ch)
			}
			if tc.challe && ch != `Bearer realm="https://id.example/\"x\""` {
				t.Errorf("challenge %q", ch)
			}
			if wantRetry := tc.code == 503; wantRetry != (w.Header().Get("Retry-After") == "30") {
				t.Errorf("Retry-After %q", w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestRetryAfterIsConfigurable(t *testing.T) {
	mw, err := New(Config[user, service]{Verifier: fakeVerifier{userKeys: true, serviceKeys: true},
		UserOK: func(user) bool { return true }, ServiceOK: AnyService[service], KeysUnavailable: errKeys,
		RetryAfter: 1500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(mw(ok), "Bearer x"); w.Header().Get("Retry-After") != "2" { // rounded up
		t.Errorf("Retry-After %q", w.Header().Get("Retry-After"))
	}
}

func TestAnyService(t *testing.T) {
	if !AnyService(service{client: "stranger"}) {
		t.Error("refused")
	}
}

// Every policy is explicit: a nil one would quietly admit everyone, or
// sign everyone out in an identity outage.
func TestNewRefusesAMissingPart(t *testing.T) {
	full := Config[user, service]{Verifier: fakeVerifier{}, UserOK: func(user) bool { return true },
		ServiceOK: AnyService[service], KeysUnavailable: errKeys}
	for name, edit := range map[string]func(*Config[user, service]){
		"Verifier":        func(c *Config[user, service]) { c.Verifier = nil },
		"UserOK":          func(c *Config[user, service]) { c.UserOK = nil },
		"ServiceOK":       func(c *Config[user, service]) { c.ServiceOK = nil },
		"KeysUnavailable": func(c *Config[user, service]) { c.KeysUnavailable = nil },
		"RetryAfter":      func(c *Config[user, service]) { c.RetryAfter = -time.Second },
	} {
		c := full
		edit(&c)
		if _, err := New(c); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := New(full); err != nil {
		t.Error(err)
	}
}

// Package apiauth is the Bearer-token middleware for an API that both
// people and services call: a request needs a valid user token whose
// user may in, or a valid service (client-credentials) token whose client
// may in.
//
// It takes the verifier as an interface over the claim types, so
// identity/common/auth.JWKSVerifier fits as it is and this module takes
// no dependency on identity:
//
//	mw, err := apiauth.New(apiauth.Config[*auth.TokenClaims, *auth.ServiceTokenClaims]{
//		Verifier:        verifier,
//		UserOK:          func(c *auth.TokenClaims) bool { return c.IsActive },
//		ServiceOK:       apiauth.AnyService[*auth.ServiceTokenClaims],
//		KeysUnavailable: auth.ErrKeysUnavailable,
//	})
//
// A token that couldn't be checked (the verifier's keys are unavailable)
// is a 503 with Retry-After, never a 401: a 401 tells the client to
// discard its credentials, so answering one in an identity outage would
// sign everyone out over tokens nobody examined.
package apiauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sweeney/common/web/serve"
)

// Verifier checks the two kinds of token. Each parser refuses the other
// kind's token.
type Verifier[U, S any] interface {
	Parse(ctx context.Context, token string) (U, error)
	ParseServiceToken(ctx context.Context, token string) (S, error)
	// Issuer is the realm of the 401's challenge.
	Issuer() string
}

// Config is the middleware's policy. Every field but RetryAfter is
// required: a missing policy would quietly admit everyone, or sign
// everyone out in an identity outage.
type Config[U, S any] struct {
	Verifier Verifier[U, S]
	// UserOK says whether a valid user token's user may in (for
	// identity, IsActive).
	UserOK func(U) bool
	// ServiceOK says whether a valid service token's client may in;
	// AnyService admits every one. A refused client gets a 403.
	ServiceOK func(S) bool
	// KeysUnavailable is the verifier's error for a token it couldn't
	// check (identity's auth.ErrKeysUnavailable), matched with errors.Is.
	KeysUnavailable error
	// RetryAfter is the 503's Retry-After; 0 is 30 s. The verifier
	// rate-limits its own refetches, so a client retrying sooner would
	// mostly meet the same failure.
	RetryAfter time.Duration
}

// AnyService admits every valid service token.
func AnyService[S any](S) bool { return true }

// New checks c and returns the middleware.
func New[U, S any](c Config[U, S]) (func(http.Handler) http.Handler, error) {
	switch {
	case c.Verifier == nil:
		return nil, errors.New("apiauth: Verifier is required")
	case c.UserOK == nil:
		return nil, errors.New("apiauth: UserOK is required")
	case c.ServiceOK == nil:
		return nil, errors.New("apiauth: ServiceOK is required (AnyService admits every client)")
	case c.KeysUnavailable == nil:
		return nil, errors.New("apiauth: KeysUnavailable is required")
	case c.RetryAfter < 0:
		return nil, fmt.Errorf("apiauth: RetryAfter %v is negative", c.RetryAfter)
	}
	retry := c.RetryAfter
	if retry == 0 {
		retry = 30 * time.Second
	}
	retryAfter := strconv.Itoa(int((retry + time.Second - 1) / time.Second))
	challenge := `Bearer realm="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(c.Verifier.Issuer()) + `"`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reject := func() {
				w.Header().Set("WWW-Authenticate", challenge)
				serve.WriteError(w, http.StatusUnauthorized, "unauthorized")
			}
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				reject()
				return
			}
			u, userErr := c.Verifier.Parse(r.Context(), token)
			if userErr == nil && c.UserOK(u) {
				next.ServeHTTP(w, r)
				return
			}
			s, svcErr := c.Verifier.ParseServiceToken(r.Context(), token)
			if svcErr == nil {
				if c.ServiceOK(s) {
					next.ServeHTTP(w, r)
					return
				}
				serve.WriteError(w, http.StatusForbidden, "forbidden")
				return
			}
			// Either parser can be the one that reached the keys: each
			// refuses the other kind before it looks at the signature. No
			// challenge on a 503: the credentials aren't in question. Not
			// logged: the verifier logs each failed fetch.
			if errors.Is(userErr, c.KeysUnavailable) || errors.Is(svcErr, c.KeysUnavailable) {
				w.Header().Set("Retry-After", retryAfter)
				serve.WriteError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
				return
			}
			reject()
		})
	}, nil
}

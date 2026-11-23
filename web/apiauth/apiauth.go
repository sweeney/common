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
//		Verifier: verifier,
//		UserOK: func(c *auth.TokenClaims) error {
//			if !c.IsActive {
//				return &apiauth.Refusal{Status: 401, Code: "invalid_token", Description: "user account is not active"}
//			}
//			return nil
//		},
//		ServiceOK:       apiauth.AnyService[*auth.ServiceTokenClaims],
//		KeysUnavailable: auth.ErrKeysUnavailable,
//		TokenExpired:    auth.ErrTokenExpired,
//	})
//
// A token goes to one parser, by its JOSE header's typ (ServiceTyp is a
// service's), so each costs one signature check. Answers follow RFC 6750:
// no token is a 401 whose challenge has no error code; a token that fails
// is a 401 invalid_token ("token expired" when it has); a policy refusal
// is the Refusal's 401 or 403 (insufficient_scope by default, with its
// scope). A token that couldn't be checked (the verifier's keys are
// unavailable) is a 503 with Retry-After and no challenge, never a 401: a
// 401 tells the client to discard its credentials, so answering one in an
// identity outage would sign everyone out over tokens nobody examined.
//
// Descriptions are the Refusal's or this package's constants: nothing of
// the presented token is reflected back.
package apiauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sweeney/common/web/serve"
)

// Verifier checks the two kinds of token.
type Verifier[U, S any] interface {
	Parse(ctx context.Context, token string) (U, error)
	ParseServiceToken(ctx context.Context, token string) (S, error)
	// Issuer is the realm of the challenge.
	Issuer() string
}

// Refusal is a policy's answer to a valid token: the status (401 or 403;
// anything else is 403) and the challenge's error, error_description and
// scope (each left out when empty).
type Refusal struct {
	Status      int
	Code        string
	Description string
	Scope       string
}

func (r *Refusal) Error() string { return fmt.Sprintf("%d %s: %s", r.Status, r.Code, r.Description) }

// Config is the middleware's policy. Verifier, UserOK, ServiceOK and
// KeysUnavailable are required: a missing policy would quietly admit
// everyone, or sign everyone out in an identity outage.
type Config[U, S any] struct {
	Verifier Verifier[U, S]
	// UserOK says whether a valid user token's user may in: nil admits, a
	// *Refusal is answered as it says, any other error is a 401
	// invalid_token ("user is not permitted"; its text is never sent).
	UserOK func(U) error
	// ServiceOK says whether a valid service token's client may in: nil
	// admits, a *Refusal is answered as it says, any other error is a 403
	// insufficient_scope. AnyService admits every client.
	ServiceOK func(S) error
	// KeysUnavailable is the verifier's error for a token it couldn't
	// check (identity's auth.ErrKeysUnavailable), matched with errors.Is.
	KeysUnavailable error
	// TokenExpired is the verifier's error for an expired token
	// (identity's auth.ErrTokenExpired); nil reports expiry as invalid.
	TokenExpired error
	// ServiceTyp is the JOSE typ of a service token; "" is "at+jwt"
	// (RFC 9068, as identity issues them).
	ServiceTyp string
	// RetryAfter is the 503's Retry-After; 0 is 30 s. The verifier
	// rate-limits its own refetches, so a client retrying sooner would
	// mostly meet the same failure.
	RetryAfter time.Duration
	// OnResult, when set, hears every request's outcome: for counters and
	// logs. It runs before the response or the next handler.
	OnResult func(*http.Request, Result[U, S])
}

// Outcome is what became of a request's credentials.
type Outcome int

const (
	Missing         Outcome = iota // no Bearer token
	Invalid                        // a token the verifier refused
	Expired                        // an expired token
	UserRefused                    // a valid user token UserOK refused
	ServiceRefused                 // a valid service token ServiceOK refused
	Unavailable                    // a token that couldn't be checked: 503
	UserAccepted                   // in, as a user
	ServiceAccepted                // in, as a service
)

func (o Outcome) String() string {
	switch o {
	case Missing:
		return "missing"
	case Invalid:
		return "invalid"
	case Expired:
		return "expired"
	case UserRefused:
		return "user refused"
	case ServiceRefused:
		return "service refused"
	case Unavailable:
		return "unavailable"
	case UserAccepted:
		return "user accepted"
	case ServiceAccepted:
		return "service accepted"
	}
	return "Outcome(" + strconv.Itoa(int(o)) + ")"
}

// Result is one request's outcome: the claims when the token was valid
// (the other kind's are zero), the refusal, or the verifier's error.
type Result[U, S any] struct {
	Outcome Outcome
	User    U
	Service S
	Refusal *Refusal
	Err     error
}

// AnyService admits every valid service token.
func AnyService[S any](S) error { return nil }

type userKey[U any] struct{}
type serviceKey[S any] struct{}

// UserFrom is the user's claims, in a request the middleware let in as a
// user.
func UserFrom[U any](ctx context.Context) (U, bool) {
	u, ok := ctx.Value(userKey[U]{}).(U)
	return u, ok
}

// ServiceFrom is the service's claims, in a request the middleware let in
// as a service.
func ServiceFrom[S any](ctx context.Context) (S, bool) {
	s, ok := ctx.Value(serviceKey[S]{}).(S)
	return s, ok
}

const (
	codeInvalidToken      = "invalid_token"
	codeInsufficientScope = "insufficient_scope"
	descInvalid           = "token is malformed, or signed by a key this server does not trust"
	descExpired           = "token expired"
)

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
	typ := c.ServiceTyp
	if typ == "" {
		typ = "at+jwt"
	}
	realm := `Bearer realm="` + quoteSafe(c.Verifier.Issuer()) + `"`

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			report := func(res Result[U, S]) {
				if c.OnResult != nil {
					c.OnResult(r, res)
				}
			}
			// refuse answers a token the verifier refused.
			refuse := func(err error) {
				switch {
				case errors.Is(err, c.KeysUnavailable):
					report(Result[U, S]{Outcome: Unavailable, Err: err})
					w.Header().Set("Retry-After", retryAfter)
					serve.WriteError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
				case c.TokenExpired != nil && errors.Is(err, c.TokenExpired):
					report(Result[U, S]{Outcome: Expired, Err: err})
					challenge(w, realm, &Refusal{Status: http.StatusUnauthorized, Code: codeInvalidToken, Description: descExpired})
				default:
					report(Result[U, S]{Outcome: Invalid, Err: err})
					challenge(w, realm, &Refusal{Status: http.StatusUnauthorized, Code: codeInvalidToken, Description: descInvalid})
				}
			}

			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				// RFC 6750 §3.1: no credentials, no error code.
				report(Result[U, S]{Outcome: Missing})
				challenge(w, realm, &Refusal{Status: http.StatusUnauthorized})
				return
			}
			if joseTyp(token) == typ {
				s, err := c.Verifier.ParseServiceToken(r.Context(), token)
				if err != nil {
					refuse(err)
					return
				}
				if err := c.ServiceOK(s); err != nil {
					rf := refusal(err, http.StatusForbidden, codeInsufficientScope, "client is not permitted to call this API")
					report(Result[U, S]{Outcome: ServiceRefused, Service: s, Refusal: rf, Err: err})
					challenge(w, realm, rf)
					return
				}
				report(Result[U, S]{Outcome: ServiceAccepted, Service: s})
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serviceKey[S]{}, s)))
				return
			}
			u, err := c.Verifier.Parse(r.Context(), token)
			if err != nil {
				refuse(err)
				return
			}
			if err := c.UserOK(u); err != nil {
				rf := refusal(err, http.StatusUnauthorized, codeInvalidToken, "user is not permitted")
				report(Result[U, S]{Outcome: UserRefused, User: u, Refusal: rf, Err: err})
				challenge(w, realm, rf)
				return
			}
			report(Result[U, S]{Outcome: UserAccepted, User: u})
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey[U]{}, u)))
		})
	}, nil
}

// refusal is err's Refusal, its status made 401 or 403; or, for any other
// error, the default (whose text is never sent).
func refusal(err error, status int, code, desc string) *Refusal {
	var rf *Refusal
	if !errors.As(err, &rf) {
		return &Refusal{Status: status, Code: code, Description: desc}
	}
	out := *rf
	if out.Status != http.StatusUnauthorized && out.Status != http.StatusForbidden {
		out.Status = http.StatusForbidden
	}
	return &out
}

// challenge writes the WWW-Authenticate and the error body for rf.
func challenge(w http.ResponseWriter, realm string, rf *Refusal) {
	var b strings.Builder
	b.WriteString(realm)
	for _, p := range [][2]string{{"error", rf.Code}, {"error_description", rf.Description}, {"scope", rf.Scope}} {
		if p[1] != "" {
			fmt.Fprintf(&b, `, %s="%s"`, p[0], quoteSafe(p[1]))
		}
	}
	w.Header().Set("WWW-Authenticate", b.String())
	if rf.Status == http.StatusForbidden {
		serve.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	serve.WriteError(w, http.StatusUnauthorized, "unauthorized")
}

// quoteSafe makes s safe in a quoted-string auth-param: a quote,
// backslash or control character is dropped, not escaped, so nothing
// (the realm is operator input) can end the string and pose as further
// parameters.
func quoteSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// bearerToken is the credential in an Authorization value. The scheme is
// case-insensitive and may be followed by spaces or tabs (RFC 7235 §2.1);
// at least one, so "BearerXYZ" isn't the token XYZ.
func bearerToken(h string) (string, bool) {
	const scheme = "bearer"
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) || (h[len(scheme)] != ' ' && h[len(scheme)] != '\t') {
		return "", false
	}
	t := strings.Trim(h[len(scheme):], " \t")
	return t, t != ""
}

// joseTyp is a JWT's header typ, or "" when it has none or isn't a JWT.
// Unverified: it only routes the token to the parser that checks it.
func joseTyp(token string) string {
	h, _, ok := strings.Cut(token, ".")
	if !ok {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(h)
	if err != nil {
		return ""
	}
	var hdr struct {
		Typ string `json:"typ"`
	}
	if json.Unmarshal(b, &hdr) != nil {
		return ""
	}
	return hdr.Typ
}

package webauth

import (
	"errors"
	"net/http"
	"strings"

	"latere.ai/x/pkg/jwtauth"

	"github.com/latere-ai/replichai/internal/httpapi"
)

// TokenValidator verifies a bearer credential and reports who presented it.
// It is an interface so the middleware is testable without a signing key or a
// JWKS endpoint; jwtauth.Validator satisfies it.
type TokenValidator interface {
	Validate(raw string) (*jwtauth.Claims, error)
}

// NewValidator builds the real validator: RS256 against the issuer's JWKS,
// with the issuer and the audience pinned. An audience that is not this
// deployment is refused even where the token is validly signed by the same
// issuer, which is why the CLI mints an audience-scoped actor token.
func NewValidator(issuer, jwksURL string, audiences []string) *jwtauth.Validator {
	return jwtauth.New(jwtauth.Config{JWKSURL: jwksURL, Issuer: issuer, Audiences: audiences})
}

// Bearer authenticates a request presenting an Authorization: Bearer
// credential, and places the same identity the cookie path would (045j).
//
// One principal, two credential sources: nothing downstream learns which one
// answered, because nothing downstream should behave differently for the same
// person on a laptop and the same person in a terminal.
//
// Precedence is fixed and total. A request carrying a bearer is authenticated
// by the bearer: the cookie is not consulted, and a failed bearer is a 401
// rather than a fall-through. A fall-through would let an attacker-supplied
// header downgrade a cookie session, or a stale cookie rescue a revoked token.
//
// A nil validator — a deployment that has configured no bearer path — refuses
// every bearer rather than accepting an unverified one.
func Bearer(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, present := bearerToken(r)
			if !present {
				next.ServeHTTP(w, r)
				return
			}
			if validator == nil {
				httpapi.WriteError(w, http.StatusUnauthorized, "bearer_unsupported")
				return
			}
			claims, err := validator.Validate(raw)
			if err != nil || claims == nil || strings.TrimSpace(claims.Sub) == "" {
				httpapi.WriteError(w, http.StatusUnauthorized, reasonFor(err))
				return
			}
			// The cookie is not consulted. Removing it here is what makes
			// that true for every handler downstream rather than only for
			// the identity the gate reads.
			r.Header.Del("Cookie")
			next.ServeHTTP(w, r.WithContext(httpapi.WithIdentity(r.Context(), identityFromClaims(claims))))
		})
	}
}

// identityFromClaims is the one mapping from a verified credential to a
// principal. The cookie path fills the same fields from the same claim names,
// so the two sources cannot disagree about what identifies a person.
func identityFromClaims(claims *jwtauth.Claims) httpapi.Identity {
	return httpapi.Identity{Sub: claims.Sub, Email: claims.Email}
}

// bearerToken reads the credential, reporting whether one was presented at
// all. Another scheme is not a bearer and is left alone.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

// reasonFor names why a credential was refused, in the machine-readable
// vocabulary the gate already uses.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, jwtauth.ErrTokenExpired):
		return "token_expired"
	case errors.Is(err, jwtauth.ErrInvalidAudience):
		return "wrong_audience"
	case errors.Is(err, jwtauth.ErrInvalidIssuer):
		return "wrong_issuer"
	case errors.Is(err, jwtauth.ErrInvalidSignature), errors.Is(err, jwtauth.ErrUnsupportedAlg):
		return "bad_signature"
	default:
		return "not_authenticated"
	}
}

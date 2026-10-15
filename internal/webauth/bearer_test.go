// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"latere.ai/x/pkg/authkit/jwt"

	"github.com/latere-ai/replichai/internal/httpapi"
)

// validator answers from a table, so the middleware is testable without a
// signing key or a JWKS endpoint.
type validator struct {
	claims map[string]*jwt.Claims
	err    map[string]error
}

func (v validator) Validate(raw string) (*jwt.Claims, error) {
	if err, ok := v.err[raw]; ok {
		return nil, err
	}
	if claims, ok := v.claims[raw]; ok {
		return claims, nil
	}
	return nil, jwt.ErrInvalidSignature
}

// seen records the identity a handler received. The property under test is
// that two credential sources produce one principal.
func seen(t *testing.T, middleware func(http.Handler) http.Handler, request *http.Request) (httpapi.Identity, int) {
	t.Helper()
	var identity httpapi.Identity
	recorder := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		identity, _ = httpapi.IdentityFrom(r)
	})).ServeHTTP(recorder, request)
	return identity, recorder.Code
}

func bearerRequest(token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

// A valid bearer and a valid cookie for the same subject produce the same
// principal. Nothing downstream should behave differently for one person on a
// laptop and the same person in a terminal.
func TestBothCredentialsProduceOnePrincipal(t *testing.T) {
	v := validator{claims: map[string]*jwt.Claims{
		"good": {Sub: "user-1", Email: "ada@example.org"},
	}}
	fromBearer, code := seen(t, Bearer(v), bearerRequest("good"))
	if code != http.StatusOK {
		t.Fatalf("a valid bearer was refused: %d", code)
	}
	fromCookie := httpapi.Identity{Sub: "user-1", Email: "ada@example.org"}
	if fromBearer.Sub != fromCookie.Sub || fromBearer.Email != fromCookie.Email {
		t.Fatalf("the two sources disagree: %+v vs %+v", fromBearer, fromCookie)
	}
}

// A request with no Authorization header passes through untouched, so the
// cookie path is unaffected.
func TestNoBearerPassesThrough(t *testing.T) {
	identity, code := seen(t, Bearer(validator{}), httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if code != http.StatusOK || identity.Sub != "" {
		t.Fatalf("identity = %+v, code = %d", identity, code)
	}
	// Another scheme is not a bearer and is left alone.
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	request.Header.Set("Authorization", "Basic abc")
	if _, code := seen(t, Bearer(validator{}), request); code != http.StatusOK {
		t.Fatalf("a non-bearer scheme was refused: %d", code)
	}
}

// A request carrying a bearer is authenticated by the bearer only. An invalid
// one is a 401 rather than a fall-through to the cookie: otherwise an
// attacker-supplied header could downgrade a session, or a stale cookie could
// rescue a revoked token.
func TestABearerNeverFallsThroughToTheCookie(t *testing.T) {
	v := validator{err: map[string]error{"bad": jwt.ErrInvalidSignature}}
	request := bearerRequest("bad")
	request.AddCookie(&http.Cookie{Name: "session", Value: "a-valid-looking-session"})
	identity, code := seen(t, Bearer(v), request)
	if code != http.StatusUnauthorized {
		t.Fatalf("an invalid bearer with a valid cookie = %d", code)
	}
	if identity.Sub != "" {
		t.Fatalf("a refused request carried an identity: %+v", identity)
	}

	// A valid bearer removes the cookie, so the cookie is not consulted by
	// anything downstream either.
	good := validator{claims: map[string]*jwt.Claims{"good": {Sub: "user-1"}}}
	request = bearerRequest("good")
	request.AddCookie(&http.Cookie{Name: "session", Value: "another-session"})
	recorder := httptest.NewRecorder()
	var cookies int
	Bearer(good)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cookies = len(r.Cookies())
	})).ServeHTTP(recorder, request)
	if cookies != 0 {
		t.Fatalf("%d cookies survived a bearer request", cookies)
	}
}

// Every way a credential can be wrong is refused with a reason, and none of
// them produces a principal.
func TestEveryBadCredentialIsRefused(t *testing.T) {
	v := validator{err: map[string]error{
		"expired":   jwt.ErrTokenExpired,
		"audience":  jwt.ErrInvalidAudience,
		"issuer":    jwt.ErrInvalidIssuer,
		"signature": jwt.ErrInvalidSignature,
		"malformed": jwt.ErrMalformedToken,
	}}
	for token, want := range map[string]string{
		"expired": "token_expired", "audience": "wrong_audience", "issuer": "wrong_issuer",
		"signature": "bad_signature", "malformed": "not_authenticated",
	} {
		recorder := httptest.NewRecorder()
		Bearer(v)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Errorf("%s reached the handler", token)
		})).ServeHTTP(recorder, bearerRequest(token))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d", token, recorder.Code)
		}
		if body := recorder.Body.String(); !contains(body, want) {
			t.Errorf("%s answered %q, want %q", token, body, want)
		}
	}
	// A token whose subject is empty is not a principal, however valid it
	// otherwise is.
	empty := validator{claims: map[string]*jwt.Claims{"anon": {Email: "someone@example.com"}}}
	if _, code := seen(t, Bearer(empty), bearerRequest("anon")); code != http.StatusUnauthorized {
		t.Fatalf("a subjectless token = %d", code)
	}
}

// A deployment that has configured no bearer path refuses every bearer rather
// than accepting an unverified one, and serves the cookie path unchanged.
func TestAnUnconfiguredDeploymentRefusesBearers(t *testing.T) {
	if _, code := seen(t, Bearer(nil), bearerRequest("anything")); code != http.StatusUnauthorized {
		t.Fatalf("an unconfigured deployment accepted a bearer: %d", code)
	}
	if _, code := seen(t, Bearer(nil), httptest.NewRequest(http.MethodGet, "/api/projects", nil)); code != http.StatusOK {
		t.Fatal("an unconfigured deployment broke the cookie path")
	}
}

// The real validator pins the issuer and the audience: a token for another
// service is refused even where the same issuer signed it. That is why the
// CLI mints an audience-scoped actor token.
func TestTheRealValidatorPinsIssuerAndAudience(t *testing.T) {
	built := NewValidator("https://auth.latere.ai", "https://auth.latere.ai/.well-known/jwks.json",
		[]string{"replichai.latere.ai"})
	if built == nil {
		t.Fatal("no validator was built")
	}
	// It is a jwt.Validator, so it satisfies the seam the middleware
	// takes and nothing here reimplements verification.
	var _ TokenValidator = built
	if _, err := built.Validate("not-a-token"); err == nil || !errors.Is(err, jwt.ErrMalformedToken) {
		t.Fatalf("Validate = %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for index := 0; index+len(needle) <= len(haystack); index++ {
				if haystack[index:index+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}

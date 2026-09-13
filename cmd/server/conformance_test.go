// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"latere.ai/x/pkg/authkit"
	"latere.ai/x/pkg/authkit/conformance"

	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/webauth"
)

// bearerAuthenticator runs the server's bearer middleware over a request
// and reports the identity it placed, so the family's suite can judge the
// production path as an authkit.Authenticator.
type bearerAuthenticator struct {
	mw func(http.Handler) http.Handler
}

func (b bearerAuthenticator) Authenticate(r *http.Request) (authkit.Identity, error) {
	var got httpapi.Identity
	var seen bool
	rec := httptest.NewRecorder()
	b.mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = httpapi.IdentityFrom(r)
	})).ServeHTTP(rec, r)
	if !seen || rec.Code != http.StatusOK {
		return authkit.Identity{}, &refused{code: rec.Code, body: rec.Body.String()}
	}
	return authkit.Identity{Sub: got.Sub, Email: got.Email, OrgID: got.OrgID, Roles: got.Roles, PrincipalType: authkit.PrincipalType(got.PrincipalType)}, nil
}

type refused struct {
	code int
	body string
}

func (e *refused) Error() string { return "refused: " + strings.TrimSpace(e.body) }

// TestConformance runs the family's rule R2 against the bearer path the
// server installs: replichai.latere.ai is admitted and nothing else, and the
// issuer is called for its key set alone.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Service{
		Audience: "replichai.latere.ai",
		New: func(t testing.TB, issuerURL, jwksURL string) authkit.Authenticator {
			v, err := bearerValidator(issuerURL, jwksURL, "replichai.latere.ai")
			if err != nil {
				t.Fatalf("bearerValidator: %v", err)
			}
			return bearerAuthenticator{mw: webauth.Bearer(v)}
		},
	})
}

func TestBearerValidatorNeedsAnAudience(t *testing.T) {
	if _, err := bearerValidator("https://auth.test", "https://auth.test/jwks", ""); err == nil || !strings.Contains(err.Error(), "AUTH_AUDIENCE") {
		t.Fatalf("an empty audience must be refused, naming the variable: %v", err)
	}
}

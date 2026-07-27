// Package webauth wires the access model (006c) to the HTTP layer: the gate
// policy the httpapi seam mounts, and — later — the OIDC login middleware that
// resolves an identity onto the request. It is the one place that imports both
// internal/httpapi and internal/access, so neither imports the other.
package webauth

import (
	"net/http"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// Gate builds the fail-closed gate policy httpapi mounts on its operator routes.
// It reads the identity the auth middleware placed on the request and resolves
// the grant per request — never caching a tier in a session — so a revoked
// grant locks its holder out on the very next request.
//
// The reasons are machine-readable: not_authenticated when there is no verified
// identity, not_invited when there is one but no grant.
func Gate(a access.Access) httpapi.Gate {
	return httpapi.GateFunc(func(r *http.Request) (bool, string) {
		id, ok := httpapi.IdentityFrom(r)
		if !ok || id.Sub == "" {
			return false, "not_authenticated"
		}
		_, granted, err := a.Resolve(r.Context(), access.Principal{
			Sub:   id.Sub,
			Email: id.Email,
		})
		if err != nil || !granted {
			return false, "not_invited"
		}
		return true, ""
	})
}

// RequireTier guards a route that needs a tier above plain admission — the
// admin surface (grant, revoke, promote) needs TierAdmin. It resolves per
// request and answers insufficient_tier for a granted-but-insufficient
// principal, so the check holds even when a request is forged past the UI.
func RequireTier(a access.Access, min access.Tier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := httpapi.IdentityFrom(r)
			if !ok || id.Sub == "" {
				httpapi.WriteError(w, http.StatusForbidden, "not_authenticated")
				return
			}
			tier, granted, err := a.Resolve(r.Context(), access.Principal{
				Sub:   id.Sub,
				Email: id.Email,
			})
			if err != nil || !granted {
				httpapi.WriteError(w, http.StatusForbidden, "not_invited")
				return
			}
			if tier < min {
				httpapi.WriteError(w, http.StatusForbidden, "insufficient_tier")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

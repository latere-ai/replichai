package webauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// Admin serves the platform-admin access endpoints: list, grant, revoke. Each
// resolves the caller's own grant through the access store, which refuses
// anyone below admin tier — so the enforcement is server-side even if the route
// were reached without the tier guard.
type Admin struct {
	access access.Access
}

// NewAdmin builds the admin handlers.
func NewAdmin(acc access.Access) *Admin { return &Admin{access: acc} }

// List returns every grant.
func (a *Admin) List(w http.ResponseWriter, r *http.Request) {
	grants, err := a.access.List(r.Context(), principalFrom(r))
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, grants)
}

// Grant seeds or promotes a grant by email.
func (a *Admin) Grant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Tier  string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	tier, ok := ParseTier(body.Tier)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_tier")
		return
	}
	if err := a.access.Grant(r.Context(), principalFrom(r), body.Email, tier); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": strings.ToLower(strings.TrimSpace(body.Email)), "tier": tier.String()})
}

// Revoke removes a grant by email.
func (a *Admin) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := a.access.Revoke(r.Context(), principalFrom(r), r.PathValue("email")); err != nil {
		writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ParseTier maps a tier name to a Tier, rejecting the unknown and the fail-safe
// "none" (which is not a grantable tier).
func ParseTier(s string) (access.Tier, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "user":
		return access.TierUser, true
	case "admin":
		return access.TierAdmin, true
	default:
		return access.TierNone, false
	}
}

// principalFrom reads the authenticated caller as an access.Principal.
func principalFrom(r *http.Request) access.Principal {
	id, _ := httpapi.IdentityFrom(r)
	return access.Principal{Sub: id.Sub, Email: id.Email}
}

// writeAdminError maps an access error to a status: a non-admin caller is
// refused not_admin, a missing email is a bad request, anything else a 500.
func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrNotAdmin):
		httpapi.WriteError(w, http.StatusForbidden, "not_admin")
	case errors.Is(err, access.ErrNoEmail):
		httpapi.WriteError(w, http.StatusBadRequest, "missing_email")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "grant_failed")
	}
}

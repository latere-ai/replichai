package webauth

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

// Admin serves the platform-admin endpoints: the access list, grant, and
// revoke (006c), plus the platform settings row and per-person quota top-ups
// (006k). Each resolves the caller's own grant through the store, which refuses
// anyone below admin tier — so the enforcement is server-side even if the route
// were reached without the tier guard.
type Admin struct {
	access   access.Access
	platform platform.Store
}

// NewAdmin builds the admin handlers. A nil platform store disables the
// settings and top-up endpoints (they answer 503), for a deployment or a test
// that wires only the access half.
func NewAdmin(acc access.Access, plat platform.Store) *Admin {
	return &Admin{access: acc, platform: plat}
}

// MountAdmin registers every admin route behind the tier guard and returns the
// set it registered. Returning them is the point: a test drives each one as a
// plain user and asserts a refusal, so a route added here without the guard —
// or added elsewhere and forgotten — fails the test rather than shipping open
// (006k AC1). It is the same trick the operator router's gated set plays.
func MountAdmin(mux *http.ServeMux, guard func(http.Handler) http.Handler, a *Admin) []httpapi.Route {
	routes := []struct {
		httpapi.Route
		h http.HandlerFunc
	}{
		{httpapi.Route{Method: "GET", Pattern: "/admin/access"}, a.List},
		{httpapi.Route{Method: "POST", Pattern: "/admin/access"}, a.Grant},
		{httpapi.Route{Method: "DELETE", Pattern: "/admin/access/{email}"}, a.Revoke},
		{httpapi.Route{Method: "PUT", Pattern: "/admin/access/{email}/quota"}, a.SetTopup},
		{httpapi.Route{Method: "GET", Pattern: "/admin/settings"}, a.Settings},
		{httpapi.Route{Method: "PUT", Pattern: "/admin/settings"}, a.SetSettings},
	}
	out := make([]httpapi.Route, 0, len(routes))
	for _, r := range routes {
		mux.Handle(r.Method+" "+r.Pattern, guard(r.h))
		out = append(out, r.Route)
	}
	return out
}

// GrantView is one row of the admin's people table: the ACL row, plus whether
// it has bound to a principal yet and any quota headroom granted on top of the
// platform default (006k). It is a view, never a stored shape.
type GrantView struct {
	Email     string    `json:"email"`
	Tier      string    `json:"tier"`
	Bound     bool      `json:"bound"`
	GrantedBy string    `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at,omitzero"`
	Topup     int       `json:"topup,omitempty"`
}

// List returns every grant, with its quota top-up merged in.
func (a *Admin) List(w http.ResponseWriter, r *http.Request) {
	caller := principalFrom(r)
	grants, err := a.access.List(r.Context(), caller)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	topups := map[string]int{}
	if a.platform != nil {
		list, err := a.platform.Topups(r.Context(), caller)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		for _, t := range list {
			topups[t.Email] = t.Extra
		}
	}
	out := make([]GrantView, 0, len(grants))
	for _, g := range grants {
		out = append(out, GrantView{
			Email:     g.Email,
			Tier:      g.Tier.String(),
			Bound:     g.Principal != "",
			GrantedBy: g.GrantedBy,
			GrantedAt: g.GrantedAt,
			Topup:     topups[g.Email],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// Settings reads the platform policy row.
func (a *Admin) Settings(w http.ResponseWriter, r *http.Request) {
	if a.platform == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_platform_store")
		return
	}
	// The read is fail-closed in the store: an error still yields the
	// conservative default, so the console shows the number that is in force
	// rather than nothing at all.
	s, err := a.platform.Settings(r.Context())
	if err != nil {
		log.Printf("admin: read platform settings: %v", err)
	}
	writeJSON(w, http.StatusOK, s)
}

// SetSettings writes the platform policy row.
func (a *Admin) SetSettings(w http.ResponseWriter, r *http.Request) {
	if a.platform == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_platform_store")
		return
	}
	var body struct {
		DefaultSessionQuota int `json:"default_session_quota"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	next := platform.Settings{DefaultSessionQuota: body.DefaultSessionQuota}
	if err := a.platform.SetSettings(r.Context(), principalFrom(r), next); err != nil {
		writeAdminError(w, err)
		return
	}
	s, _ := a.platform.Settings(r.Context())
	writeJSON(w, http.StatusOK, s)
}

// SetTopup sets one person's quota headroom above the platform default. Zero
// removes it; the email need not have signed in yet.
func (a *Admin) SetTopup(w http.ResponseWriter, r *http.Request) {
	if a.platform == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_platform_store")
		return
	}
	var body struct {
		Extra int `json:"extra"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	email := r.PathValue("email")
	if err := a.platform.SetTopup(r.Context(), principalFrom(r), email, body.Extra); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": strings.ToLower(strings.TrimSpace(email)), "extra": body.Extra})
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

// writeAdminError maps an access or platform error to a status: a non-admin
// caller is refused not_admin, a bad argument is a bad request, anything else a
// 500. The two packages' sentinels are kept distinct in code and merged here,
// so neither has to import the other's errors.
func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrNotAdmin), errors.Is(err, platform.ErrNotAdmin):
		httpapi.WriteError(w, http.StatusForbidden, "not_admin")
	case errors.Is(err, access.ErrNoEmail), errors.Is(err, platform.ErrNoEmail):
		httpapi.WriteError(w, http.StatusBadRequest, "missing_email")
	case errors.Is(err, platform.ErrNegative):
		httpapi.WriteError(w, http.StatusBadRequest, "negative_quota")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "grant_failed")
	}
}

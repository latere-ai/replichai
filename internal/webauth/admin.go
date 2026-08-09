package webauth

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

// Admin serves the platform-admin endpoints: the access list, grant, and
// revoke (006c), the platform settings row (006k), and the credit top-up that
// is how money enters the platform at all (011). Each resolves the caller's own
// grant through the store, which refuses anyone below admin tier — so the
// enforcement is server-side even if the route were reached without the tier
// guard.
type Admin struct {
	access    access.Access
	platform  platform.Store
	credits   credit.Store
	projects  ProjectCaps
	overrides access.FreezeOverrides
}

// ProjectCaps is the sliver of the project store this console writes: one
// project's own overdraft floor. Narrow on purpose — the admin surface has no
// business renaming or publishing somebody's project.
type ProjectCaps interface {
	SetCreditCap(ctx context.Context, id string, cap int64) error
}

// WithProjectCaps enables the per-project overdraft override. Without it the
// route answers 503 and every project runs against the platform default.
func WithProjectCaps(p ProjectCaps) func(*Admin) {
	return func(a *Admin) { a.projects = p }
}

// WithOverrides enables the freeze list and the unfreeze action (022d). Without
// it those routes answer 503 and the people table cannot report a frozen state.
func WithOverrides(ov access.FreezeOverrides) func(*Admin) {
	return func(a *Admin) { a.overrides = ov }
}

// NewAdmin builds the admin handlers. A nil platform store disables the
// settings endpoints and a nil ledger the top-up (they answer 503), for a
// deployment or a test that wires only the access half.
func NewAdmin(acc access.Access, plat platform.Store, credits credit.Store, opts ...func(*Admin)) *Admin {
	a := &Admin{access: acc, platform: plat, credits: credits}
	for _, opt := range opts {
		opt(a)
	}
	return a
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
		{httpapi.Route{Method: "POST", Pattern: "/admin/access/{email}/credits"}, a.Topup},
		{httpapi.Route{Method: "GET", Pattern: "/admin/settings"}, a.Settings},
		{httpapi.Route{Method: "PUT", Pattern: "/admin/settings"}, a.SetSettings},
		{httpapi.Route{Method: "GET", Pattern: "/admin/solvency"}, a.Solvency},
		{httpapi.Route{Method: "POST", Pattern: "/admin/access/{email}/unfreeze"}, a.Unfreeze},
		{httpapi.Route{Method: "DELETE", Pattern: "/admin/access/{email}/unfreeze"}, a.Refreeze},
		{httpapi.Route{Method: "PUT", Pattern: "/admin/projects/{id}/cap"}, a.SetProjectCap},
	}
	out := make([]httpapi.Route, 0, len(routes))
	for _, r := range routes {
		mux.Handle(r.Method+" "+r.Pattern, guard(r.h))
		out = append(out, r.Route)
	}
	return out
}

// GrantView is one row of the admin's people table: the ACL row, plus whether
// it has bound to a principal yet and what their wallet holds (011). It is a
// view, never a stored shape.
type GrantView struct {
	Email     string    `json:"email"`
	Tier      string    `json:"tier"`
	Bound     bool      `json:"bound"`
	GrantedBy string    `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at,omitzero"`
	// Wallet is this person's credit balance in micro-USD — what an admin reads
	// before deciding who needs a top-up. It is keyed by email, so it is
	// readable for somebody who has never signed in.
	Wallet int64 `json:"wallet"`
	// Frozen is whether this account is in view-only mode: a negative wallet with
	// no admin override in force (022d). Derived the same way the gate derives it,
	// so the table and the gate cannot disagree.
	Frozen bool `json:"frozen"`
}

// List returns every grant, with its wallet balance and frozen state merged in.
// With ?filter=negative it returns only the accounts that owe — the debts an
// operator acts on — so finding them is not a scan down the balance column.
func (a *Admin) List(w http.ResponseWriter, r *http.Request) {
	caller := principalFrom(r)
	grants, err := a.access.List(r.Context(), caller)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	negativeOnly := r.URL.Query().Get("filter") == "negative"
	out := make([]GrantView, 0, len(grants))
	for _, g := range grants {
		v := GrantView{
			Email:     g.Email,
			Tier:      g.Tier.String(),
			Bound:     g.Principal != "",
			GrantedBy: g.GrantedBy,
			GrantedAt: g.GrantedAt,
		}
		// The wallet is read by email, which is also how it is funded — so an
		// admin can see and top up somebody who has not signed in yet, and
		// reading the table never binds a grant to the caller's principal the
		// way resolving one would.
		if a.credits != nil {
			if n, err := a.credits.Balance(r.Context(), credit.Wallet(g.Email)); err == nil {
				v.Wallet = n
			} else {
				log.Printf("admin: reading the wallet for %s: %v", g.Email, err)
			}
		}
		if negativeOnly && v.Wallet >= 0 {
			continue
		}
		// Frozen is the gate's own predicate: negative wallet, no override.
		if v.Wallet < 0 && a.overrides != nil {
			if active, err := a.overrides.OverrideActive(r.Context(), g.Email); err == nil {
				v.Frozen = !active
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// Unfreeze grants a one-shot freeze override so a negative-wallet account may
// work again (022d). It is recorded state naming the admin; the override retires
// itself when the wallet returns to solvent, so it forgives this debt only.
func (a *Admin) Unfreeze(w http.ResponseWriter, r *http.Request) {
	if a.overrides == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_override_store")
		return
	}
	email := r.PathValue("email")
	if err := a.overrides.GrantOverride(r.Context(), principalFrom(r), email); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "overridden": true})
}

// Refreeze clears a still-standing override, re-freezing a negative account an
// admin had waived. The inverse of Unfreeze, for a decision reconsidered.
func (a *Admin) Refreeze(w http.ResponseWriter, r *http.Request) {
	if a.overrides == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_override_store")
		return
	}
	email := r.PathValue("email")
	if err := a.overrides.ClearOverride(r.Context(), email); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "overridden": false})
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
		StartReserve int64 `json:"start_reserve"`
		// OverdraftCap is admin-only, here and nowhere else. It is how far below
		// zero a project may fall before a run is killed, and a number a project
		// member could read is an allowance they would plan around (011).
		OverdraftCap int64        `json:"overdraft_cap"`
		Rates        *credit.Card `json:"rates"`
		// The purchase spread and floor (022b), pointers so an omitted field
		// keeps the one in force rather than zeroing it — a console that edits
		// only the reserve must not wipe the margin.
		SpreadBps        *int64 `json:"spread_bps"`
		SpreadFixedMicro *int64 `json:"spread_fixed_micro"`
		MinTopupMicro    *int64 `json:"min_topup_micro"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	next := platform.Settings{StartReserve: body.StartReserve, OverdraftCap: body.OverdraftCap}
	// An omitted card, spread, or floor keeps the value in force, so a partial
	// edit never silently wipes a setting it did not touch.
	current, currentErr := a.platform.Settings(r.Context())
	if body.Rates != nil {
		next.Rates = *body.Rates
	} else if currentErr == nil {
		next.Rates = current.Rates
	}
	next.SpreadBps = current.SpreadBps
	next.SpreadFixedMicro = current.SpreadFixedMicro
	next.MinTopupMicro = current.MinTopupMicro
	if body.SpreadBps != nil {
		next.SpreadBps = *body.SpreadBps
	}
	if body.SpreadFixedMicro != nil {
		next.SpreadFixedMicro = *body.SpreadFixedMicro
	}
	if body.MinTopupMicro != nil {
		next.MinTopupMicro = *body.MinTopupMicro
	}
	if err := a.platform.SetSettings(r.Context(), principalFrom(r), next); err != nil {
		writeAdminError(w, err)
		return
	}
	s, _ := a.platform.Settings(r.Context())
	writeJSON(w, http.StatusOK, s)
}

// Solvency reports outstanding sold-liability: the sum of every wallet and pot
// balance — credits the platform has sold and not yet delivered as compute
// (022c). An operator watches it against what the vendor account holds and tops
// the vendor up when it needs it; it is the read that replaces coupling a
// customer payment to a vendor top-up.
func (a *Admin) Solvency(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_credits_store")
		return
	}
	total, err := a.credits.TotalOutstanding(r.Context())
	if err != nil {
		log.Printf("admin: read solvency: %v", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "solvency_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"outstanding_micro": total})
}

// Topup adds credits to one person's wallet — the only way money enters the
// platform until a payment provider posts the same ledger entry. The email need
// not have signed in yet, which is what lets an admin fund somebody before
// inviting them.
func (a *Admin) Topup(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_credit_store")
		return
	}
	var body struct {
		// Amount is micro-USD, positive. A negative top-up is not a withdrawal
		// with the sign flipped: taking credits back is a different act with a
		// different record, and it does not exist yet.
		Amount int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	email := r.PathValue("email")
	if err := a.credits.Topup(r.Context(), principalFrom(r), email, body.Amount); err != nil {
		writeAdminError(w, err)
		return
	}
	balance, _ := a.credits.Balance(r.Context(), credit.Wallet(email))
	writeJSON(w, http.StatusOK, map[string]any{
		"email": strings.ToLower(strings.TrimSpace(email)), "wallet": balance,
	})
}

// SetProjectCap overrides one project's overdraft floor; zero restores the
// platform default. Admin-only, and deliberately not the owner's: a project
// deciding how far past its own balance it may run is the one party with an
// interest in that number being large.
func (a *Admin) SetProjectCap(w http.ResponseWriter, r *http.Request) {
	if a.projects == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_project_store")
		return
	}
	var body struct {
		Cap int64 `json:"cap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if body.Cap < 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_amount")
		return
	}
	if err := a.projects.SetCreditCap(r.Context(), r.PathValue("id"), body.Cap); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "cap_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": r.PathValue("id"), "cap": body.Cap})
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
	case errors.Is(err, access.ErrNotAdmin), errors.Is(err, platform.ErrNotAdmin), errors.Is(err, credit.ErrNotAdmin):
		httpapi.WriteError(w, http.StatusForbidden, "not_admin")
	case errors.Is(err, access.ErrNoEmail), errors.Is(err, credit.ErrNoEmail):
		httpapi.WriteError(w, http.StatusBadRequest, "missing_email")
	case errors.Is(err, platform.ErrNegative), errors.Is(err, credit.ErrNotPositive):
		httpapi.WriteError(w, http.StatusBadRequest, "bad_amount")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "grant_failed")
	}
}

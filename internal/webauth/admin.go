// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

// Admin serves the platform-admin endpoints: the access list, grant and
// revoke (006c), the platform settings row (006k), and the credit top-up
// through which money enters the platform (011). Each resolves the caller's
// own grant through the store, which refuses anyone below admin tier, so the
// enforcement is server-side even if a route were reached without the tier
// guard.
type Admin struct {
	access    access.Access
	platform  platform.Store
	credits   credit.Store
	projects  ProjectCaps
	overrides access.FreezeOverrides
	sweep     PaperSweep
	// environments are the Latere environments the runtime knows, which an
	// admin chooses among (051). Nil checks no name against them.
	environments []string
	// prices is the price list the card follows. Nil for a deployment that
	// prices every model by hand.
	prices *platform.PriceList
}

// WithPriceList names the price list the card follows, so the console shows
// which rows are the list's and a save keeps them at the list's prices.
func WithPriceList(p *platform.PriceList) func(*Admin) {
	return func(a *Admin) { a.prices = p }
}

// WithLatereEnvironments names the environments the Latere runtime knows, so
// the console offers them and a list naming another is refused.
func WithLatereEnvironments(known []string) func(*Admin) {
	return func(a *Admin) { a.environments = known }
}

// PaperSweep fills in the bibliography of every paper the platform holds that is
// still missing one (037). It is a function rather than an interface because
// there is only one and it takes no arguments an admin chooses: its behavior
// is decided by the enrichment rule, not by the caller.
type PaperSweep func(ctx context.Context) (projects, filled int, err error)

// WithPaperSweep enables the bibliography sweep. Without it the route answers
// 503, which is correct for a deployment with no paper resolver.
func WithPaperSweep(f PaperSweep) func(*Admin) {
	return func(a *Admin) { a.sweep = f }
}

// ProjectCaps is the subset of the project store this console writes: one
// project's overdraft floor. It is kept narrow because the admin surface must
// not be able to rename or publish a project.
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
// settings endpoints and a nil ledger disables the top-up; both then answer
// 503. This serves a deployment or a test that wires only the access half.
func NewAdmin(acc access.Access, plat platform.Store, credits credit.Store, opts ...func(*Admin)) *Admin {
	a := &Admin{access: acc, platform: plat, credits: credits}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// MountAdmin registers every admin route behind the tier guard and returns the
// set it registered. A test drives each returned route as a plain user and
// asserts a refusal, so a route added here without the guard, or added
// elsewhere and not listed, fails the test rather than shipping open
// (006k AC1). The operator router's gated set uses the same technique.
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
		{httpapi.Route{Method: "POST", Pattern: "/admin/papers/refresh"}, a.RefreshPapers},
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
	// Wallet is this person's credit balance in micro-USD, which an admin reads
	// before deciding who needs a top-up. It is keyed by email, so it is
	// readable for somebody who has never signed in.
	Wallet int64 `json:"wallet"`
	// Frozen is whether this account is in view-only mode: a negative wallet with
	// no admin override in force (022d). Derived the same way the gate derives it,
	// so the table and the gate cannot disagree.
	Frozen bool `json:"frozen"`
}

// List returns every grant, with its wallet balance and frozen state merged in.
// With ?filter=negative it returns only the accounts with a negative balance,
// which are the ones an operator acts on, so finding them does not require
// scanning the balance column.
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
		// The wallet is read by email, which is also how it is funded. An admin
		// can therefore see and top up somebody who has not signed in yet, and
		// reading the table never binds a grant to the caller's principal the
		// way resolving one would.
		if a.credits != nil {
			if n, err := a.credits.Balance(r.Context(), credit.Wallet(g.Email)); err == nil {
				v.Wallet = n
			} else {
				slog.WarnContext(r.Context(), "admin: reading the wallet", "email", g.Email, "error", err)
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
	writeJSON(w, out)
}

// Unfreeze grants a one-shot freeze override so a negative-wallet account may
// work again (022d). The override is recorded state naming the admin. It is
// retired when the wallet returns to solvent, so it waives this debt only.
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
	writeJSON(w, map[string]any{"email": email, "overridden": true})
}

// Refreeze clears an active override, re-freezing a negative account an admin
// had waived. It is the inverse of Unfreeze.
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
	writeJSON(w, map[string]any{"email": email, "overridden": false})
}

// Settings reads the platform policy row.
func (a *Admin) Settings(w http.ResponseWriter, r *http.Request) {
	if a.platform == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_platform_store")
		return
	}
	// The read is fail-closed in the store: an error still yields the
	// conservative default, so the console shows the number in force rather
	// than nothing.
	s, err := a.platform.Settings(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "admin: reading platform settings", "error", err)
	}
	writeJSON(w, a.view(s))
}

// settingsView is the settings as the console reads them, with the Latere
// environments it may offer beside the ones in force, and the price list the
// card follows, if any. Neither is stored with the settings.
type settingsView struct {
	platform.Settings
	KnownLatereEnvironments []string                `json:"known_latere_environments,omitempty"`
	PriceList               *platform.PriceListView `json:"price_list,omitzero"`
}

func (a *Admin) view(s platform.Settings) settingsView {
	v := settingsView{Settings: s, KnownLatereEnvironments: a.environments}
	if a.prices != nil {
		list := a.prices.View()
		v.PriceList = &list
	}
	return v
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
		// The purchase spread and floor (022b) are pointers so an omitted field
		// keeps the value in force rather than zeroing it. A console that edits
		// only the reserve must not wipe the margin.
		SpreadBps        *int64 `json:"spread_bps"`
		SpreadFixedMicro *int64 `json:"spread_fixed_micro"`
		MinTopupMicro    *int64 `json:"min_topup_micro"`
		// Draft names the model that drafts a description (039). It is a
		// pointer for the same reason: a console that edits only the reserve
		// must not switch drafting off.
		Draft *draft.Config `json:"draft"`
		// LatereEnvironments are the environments a Latere session may name
		// (051). A pointer, so a console that edits only the reserve keeps
		// the list in force.
		LatereEnvironments *[]string `json:"latere_environments"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	next := platform.Settings{StartReserve: body.StartReserve, OverdraftCap: body.OverdraftCap}
	// An omitted card, spread, or floor keeps the value in force, so a partial
	// edit never silently wipes a setting it did not touch. That needs the
	// value in force: if it cannot be read, the edit is refused rather than
	// written over compiled defaults.
	current, err := a.platform.Settings(r.Context())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	next.Rates = current.Rates
	if body.Rates != nil {
		next.Rates = *body.Rates
	}
	// A row the price list prices keeps the list's prices. The next sync would
	// write them back within the hour, and until then a console that read the
	// card before the last sync would put back the prices the list replaced.
	// An empty card is left empty, so the store still falls back to the
	// compiled-in one.
	if a.prices != nil && len(next.Rates.Models) > 0 {
		if listed := a.prices.Listed(); len(listed) > 0 {
			next.Rates, _ = next.Rates.WithListed(listed)
		}
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
	next.Draft = current.Draft
	if body.Draft != nil {
		next.Draft = *body.Draft
	}
	next.LatereEnvironments = current.LatereEnvironments
	if body.LatereEnvironments != nil {
		next.LatereEnvironments = *body.LatereEnvironments
		for _, name := range next.LatereEnvironments {
			if a.environments != nil && !slices.Contains(a.environments, name) {
				httpapi.WriteError(w, http.StatusBadRequest, "bad_environments")
				return
			}
		}
	}
	if err := a.platform.SetSettings(r.Context(), principalFrom(r), next); err != nil {
		writeAdminError(w, err)
		return
	}
	s, err := a.platform.Settings(r.Context())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, a.view(s))
}

// Solvency reports outstanding sold liability: the sum of every wallet and pot
// balance, meaning credits the platform has sold and not yet delivered as
// compute (022c). An operator compares it with the vendor account's balance and
// tops the vendor up when needed. This read replaces coupling a customer
// payment to a vendor top-up.
func (a *Admin) Solvency(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_credits_store")
		return
	}
	total, err := a.credits.TotalOutstanding(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "admin: reading solvency", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "solvency_read_failed")
		return
	}
	writeJSON(w, map[string]int64{"outstanding_micro": total})
}

// Topup adds credits to one person's wallet. It is the only way money enters
// the platform until a payment provider posts the same ledger entry. The email
// need not have signed in yet, so an admin can fund somebody before inviting
// them.
func (a *Admin) Topup(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_credit_store")
		return
	}
	var body struct {
		// Amount is micro-USD and must be positive. A negative amount is
		// refused rather than treated as a withdrawal: taking credits back is
		// a different operation with a different record, and it does not
		// exist yet.
		Amount int64 `json:"amount"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "bad_request")
		return
	}
	email := r.PathValue("email")
	if err := a.credits.Topup(r.Context(), principalFrom(r), email, body.Amount); err != nil {
		writeAdminError(w, err)
		return
	}
	balance, _ := a.credits.Balance(r.Context(), credit.Wallet(email))
	writeJSON(w, map[string]any{
		"email": strings.ToLower(strings.TrimSpace(email)), "wallet": balance,
	})
}

// SetProjectCap overrides one project's overdraft floor. Zero restores the
// platform default. It is admin-only and deliberately not available to the
// owner, because the owner has an interest in setting the overdraft high.
func (a *Admin) SetProjectCap(w http.ResponseWriter, r *http.Request) {
	if a.projects == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_project_store")
		return
	}
	var body struct {
		Cap int64 `json:"cap"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
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
	writeJSON(w, map[string]any{"project": r.PathValue("id"), "cap": body.Cap})
}

// Grant seeds or promotes a grant by email.
func (a *Admin) Grant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Tier  string `json:"tier"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
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
	writeJSON(w, map[string]string{"email": strings.ToLower(strings.TrimSpace(body.Email)), "tier": tier.String()})
}

// Revoke removes a grant by email.
func (a *Admin) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := a.access.Revoke(r.Context(), principalFrom(r), r.PathValue("email")); err != nil {
		writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ParseTier maps a tier name to a Tier. It rejects unknown names and the
// fail-safe "none", which is not a grantable tier.
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
	case errors.Is(err, credit.ErrBadCard):
		httpapi.WriteError(w, http.StatusBadRequest, "bad_card")
	case errors.Is(err, platform.ErrEnvironments):
		httpapi.WriteError(w, http.StatusBadRequest, "bad_environments")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "grant_failed")
	}
}

// RefreshPapers fills in every paper the platform holds whose bibliography is
// incomplete (037), and reports what it did.
//
// It is the admin's half of the fill-in: an owner's button covers one project,
// and this covers the projects no owner has opened since the record was
// created. It is synchronous, because the count it returns is the reason to
// call it. It is safe to call twice, because enrichment never overwrites.
func (a *Admin) RefreshPapers(w http.ResponseWriter, r *http.Request) {
	if a.sweep == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "no_sweep")
		return
	}
	projects, filled, err := a.sweep(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "admin: paper sweep", "error", err)
		httpapi.WriteError(w, http.StatusBadGateway, "sweep_failed")
		return
	}
	writeJSON(w, struct {
		Projects int `json:"projects"`
		Filled   int `json:"filled"`
	}{projects, filled})
}

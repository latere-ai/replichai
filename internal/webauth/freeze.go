// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// Frozen reports whether an account is in view-only mode (022d): its wallet is
// negative and no admin override is in force. It is the single predicate the
// gate and /me both read.
//
// It reads and never writes. An override is a waiver of a debt. The top-up
// that pays the debt retires the override, through the ledger's on-solvent
// hook wired at the composition root, so a later chargeback re-freezes as a
// fresh decision. This path runs on every mutating request and every /me, and
// costs the database only a balance read.
func Frozen(ctx context.Context, credits credit.Store, ov access.FreezeOverrides, email string) (bool, error) {
	bal, err := credits.Balance(ctx, credit.Wallet(email))
	if err != nil {
		return false, err
	}
	if bal >= 0 {
		return false, nil
	}
	active, err := ov.OverrideActive(ctx, email)
	if err != nil {
		return false, err
	}
	return !active, nil
}

// FrozenWrites is every write a frozen account may still make, and the only
// place the list is kept. A frozen account keeps what it needs to see and take
// its own work, to pay what it owes, and to stop or retract what it has: it may
// read everything it could read (projects, sessions, transcripts, and the
// project archive download, which is its export), and these writes. The
// checkout is its only way to become solvent again, and the payment webhook
// carries no identity and credits the account back. Cancelling a session,
// withdrawing a publication, reclaiming credits from a project and moving a
// project to the trash spend nothing, so they stay open. A pattern names path
// parameters in braces, as the router does. Any other write is refused with 402
// account_frozen.
var FrozenWrites = []httpapi.Route{
	{Method: http.MethodPost, Pattern: "/me/credits/checkout"},
	{Method: http.MethodPost, Pattern: "/stripe/webhook"},
	{Method: http.MethodPost, Pattern: "/jobs/{id}/cancel"},
	{Method: http.MethodPost, Pattern: "/projects/{id}/private"},
	{Method: http.MethodPost, Pattern: "/projects/{id}/credits/reclaim"},
	{Method: http.MethodDelete, Pattern: "/projects/{id}"},
}

// AllowedWhileFrozen reports whether a request may pass a frozen account: any
// read, and the writes listed in FrozenWrites. Reads and CORS preflight change
// no state, so they are allowed by method; a write is allowed only by listing.
func AllowedWhileFrozen(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return slices.ContainsFunc(FrozenWrites, func(r httpapi.Route) bool {
		return r.Method == method && matchPattern(r.Pattern, path)
	})
}

// matchPattern reports whether path has the same segments as pattern, where a
// segment written in braces matches any one non-empty segment.
func matchPattern(pattern, path string) bool {
	want := strings.Split(pattern, "/")
	got := strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i, w := range want {
		if strings.HasPrefix(w, "{") && strings.HasSuffix(w, "}") {
			if got[i] == "" {
				return false
			}
			continue
		}
		if w != got[i] {
			return false
		}
	}
	return true
}

// FreezeGate refuses a write from a negative-wallet account with 402
// account_frozen unless AllowedWhileFrozen lists it, at the single point every
// request passes. A frozen account calling the API directly is therefore
// refused exactly as the UI is, while reads, including the project archive
// download, and the top-up that unfreezes it still go through. It composes inside the auth middleware, which
// has already put the identity on the request.
//
// It is a coarse check over a guarantee the ledger also keeps: spend is refused
// in the credit store whether or not this gate runs. A read error here
// therefore fails open, logging and allowing the request, rather than freezing
// every account on a transient database error. A nil ledger or override store
// makes it a pass-through, which an offline run needs.
func FreezeGate(credits credit.Store, ov access.FreezeOverrides) func(http.Handler) http.Handler {
	if credits == nil || ov == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if AllowedWhileFrozen(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			id, ok := httpapi.IdentityFrom(r)
			if !ok || id.Email == "" {
				next.ServeHTTP(w, r) // unauthenticated: the operator gate handles it
				return
			}
			frozen, err := Frozen(r.Context(), credits, ov, id.Email)
			if err != nil {
				slog.WarnContext(r.Context(), "freeze gate: failing open", "email", id.Email, "error", err)
				next.ServeHTTP(w, r) // fail open; the ledger still guards spend
				return
			}
			if frozen {
				httpapi.WriteError(w, http.StatusPaymentRequired, "account_frozen")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

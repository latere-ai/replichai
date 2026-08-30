// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"log"
	"net/http"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// Frozen reports whether an account is in view-only mode: its wallet is negative
// and no admin override is in force (022d). It also retires a spent override —
// one whose wallet has recovered to >= 0 — so a later chargeback re-freezes as a
// fresh decision. Retiring here, in the one predicate every surface reads,
// rather than at each crediting path, keeps the override's lifecycle in one
// place; the cost is that an override outlives its debt until the next solvent
// read, which is benign because a negative wallet cannot allocate regardless of
// the freeze.
func Frozen(ctx context.Context, credits credit.Store, ov access.FreezeOverrides, email string) (bool, error) {
	bal, err := credits.Balance(ctx, credit.Wallet(email))
	if err != nil {
		return false, err
	}
	if bal >= 0 {
		// Solvent: retire any override best-effort. A failed clear leaves a
		// harmless stale override, cleared on the next solvent read.
		_ = ov.ClearOverride(ctx, email)
		return false, nil
	}
	active, err := ov.OverrideActive(ctx, email)
	if err != nil {
		return false, err
	}
	return !active, nil
}

// isMutating reports whether a method changes state. Reads (and CORS preflight)
// pass a frozen account untouched.
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// allowedWhileFrozen are the two mutating paths a frozen account may still hit:
// the checkout that is its only way out, and the payment webhook (which carries
// no identity and credits the account back).
func allowedWhileFrozen(path string) bool {
	return path == "/me/credits/checkout" || path == "/stripe/webhook"
}

// FreezeGate refuses a mutating request from a negative-wallet account with
// 402 account_frozen, at the one narrow point every request passes — so a
// frozen account calling the API directly is refused exactly as the UI is,
// while reads (including the project archive download) and the top-up that thaws
// it still go through. It composes *inside* the auth middleware, which has
// already put the identity on the request.
//
// It is a coarse net over a guarantee the ledger also keeps: spend is refused in
// the credit store whether or not this gate runs, so a read error here fails
// open (log and allow) rather than freezing everybody on a database hiccup. A
// nil ledger or override store leaves it a pass-through, which an offline run
// wants.
func FreezeGate(credits credit.Store, ov access.FreezeOverrides) func(http.Handler) http.Handler {
	if credits == nil || ov == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) || allowedWhileFrozen(r.URL.Path) {
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
				log.Printf("freeze gate: %s: %v", id.Email, err)
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

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"log/slog"
	"net/http"

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

// isMutating reports whether a method changes state. Reads and CORS preflight
// pass a frozen account untouched.
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// allowedWhileFrozen reports the two mutating paths a frozen account may still
// call: the checkout, which is its only way to become solvent again, and the
// payment webhook, which carries no identity and credits the account back.
func allowedWhileFrozen(path string) bool {
	return path == "/me/credits/checkout" || path == "/stripe/webhook"
}

// FreezeGate refuses a mutating request from a negative-wallet account with
// 402 account_frozen, at the single point every request passes. A frozen
// account calling the API directly is therefore refused exactly as the UI is,
// while reads, including the project archive download, and the top-up that
// unfreezes it still go through. It composes inside the auth middleware, which
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

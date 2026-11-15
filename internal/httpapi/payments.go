// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/notify"
	"github.com/latere-ai/replichai/internal/platform"

	"latere.ai/x/pay"
	"latere.ai/x/pay/money"
)

// microPerCent converts the ledger's micro-USD to the whole cents a card
// processor charges: 1 cent is 10,000 micro-USD.
const microPerCent = 10_000

// startCheckout opens a hosted USD checkout for the credit a signed-in person is
// buying. The credited amount, the gross less the current spread, is computed
// here and carried on the checkout session, so the webhook posts it without
// recomputing, and an admin editing the spread during the purchase cannot
// change what it credits (022b). EUR presentment is done by the processor's
// Adaptive Pricing, not by this handler.
func (rt *Router) startCheckout(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r)
	var body struct {
		AmountMicro int64 `json:"amount_micro"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	// The amount is rounded down to whole cents, the processor's unit, and
	// priced from the policy in force. A failed read yields the seeded
	// defaults inside the store.
	cents := body.AmountMicro / microPerCent
	gross := cents * microPerCent
	var set platform.Settings
	if rt.policy != nil {
		set, _ = rt.policy.Settings(r.Context())
	}
	if gross <= 0 || gross < set.MinTopupMicro {
		writeError(w, http.StatusBadRequest, "below_min")
		return
	}
	credited := set.Credited(gross)

	if rt.payments == nil {
		writeError(w, http.StatusNotImplemented, "payments_unconfigured")
		return
	}
	out, err := rt.payments.CreateCheckout(r.Context(), pay.CheckoutParams{
		Email:       id.Email,
		Amount:      money.Micro(gross),
		Currency:    money.USD,
		Description: "replichai credits",
		SuccessURL:  rt.baseURL + "/wallet?checkout=success",
		CancelURL:   rt.baseURL + "/wallet",
		Meta:        map[string]string{"credited_micro": strconv.FormatInt(credited, 10)},
	})
	if errors.Is(err, pay.ErrUnconfigured) {
		writeError(w, http.StatusNotImplemented, "payments_unconfigured")
		return
	}
	if err != nil {
		slog.WarnContext(r.Context(), "checkout: creating the session", "email", id.Email, "error", err)
		writeError(w, http.StatusBadGateway, "checkout_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": out.URL})
}

// stripeWebhook is the only handler that credits a purchase (022b). It is
// outside the operator gate because the processor carries no session; the
// signature is its authentication, and the credited amount comes from the
// verified event's metadata, never from a redirect parameter. The ledger makes
// it idempotent: a retried delivery posts the same external reference and moves
// the balance once.
func (rt *Router) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if rt.payments == nil {
		w.WriteHeader(http.StatusOK) // Nothing is configured, so the event is acknowledged and dropped.
		return
	}
	body, err := io.ReadAll(r.Body)
	if tooLarge(err) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	ev, err := rt.payments.ParseWebhook(body, r.Header)
	if errors.Is(err, pay.ErrUnconfigured) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_signature")
		return
	}
	switch ev.Kind {
	case pay.KindPaid:
		if code := rt.creditPurchase(r, ev); code != http.StatusOK {
			writeError(w, code, "topup_failed") // Stripe retries on a non-2xx status.
			return
		}
	case pay.KindRefunded, pay.KindDisputed:
		if code := rt.reversePurchase(r, ev); code != http.StatusOK {
			writeError(w, code, "refund_failed") // Stripe retries on a non-2xx status.
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// creditPurchase posts the wallet credit a paid checkout earned, from the
// verified event's metadata. It returns 200 when there is nothing to post, so
// a malformed event is acknowledged rather than retried, or when the post
// succeeds, and 500 only on a store error worth retrying.
func (rt *Router) creditPurchase(r *http.Request, ev pay.Event) int {
	credited, perr := strconv.ParseInt(ev.Meta["credited_micro"], 10, 64)
	if ev.Email == "" || perr != nil || credited <= 0 {
		slog.WarnContext(r.Context(), "stripe webhook: unusable checkout metadata", "email", ev.Email, "credited", ev.Meta["credited_micro"])
		return http.StatusOK
	}
	if rt.credits == nil {
		return http.StatusOK
	}
	if err := rt.credits.TopupPaid(r.Context(), ev.Email, credited, ev.Ref, "stripe:"+ev.Ref); err != nil {
		slog.ErrorContext(r.Context(), "stripe webhook: top-up not credited", "email", ev.Email, "error", err)
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// chargeTotals reads, from a verified charge.refunded delivery, how much of the
// charge has been refunded in all and what the charge was. Both are in the
// charge's own currency, so their ratio is the share of the purchase refunded
// without converting anything. The payload is the processor's own envelope;
// the pay library keeps it on the event as Raw because it models the refund's
// amount as advisory and leaves it out for any currency but USD.
func chargeTotals(raw []byte) (refunded, charged int64, ok bool) {
	var env struct {
		Data struct {
			Object struct {
				Amount         int64 `json:"amount"`
				AmountRefunded int64 `json:"amount_refunded"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, 0, false
	}
	o := env.Data.Object
	return o.AmountRefunded, o.Amount, o.Amount > 0 && o.AmountRefunded >= 0
}

// reversePurchase takes back credit for a refunded or disputed purchase. A
// refund takes back its share of the credit, the purchase's credit times what
// has been refunded over what was charged, and a dispute takes back what is
// still unreversed, so the total for one purchase never passes what it
// credited. The credit is reversed in the units the ledger holds, not the
// currency paid, which is the FX-drift rule: a purchase converted at one rate
// and refunded at another leaves nothing unaccounted. The reversal is keyed on
// its own id so it is deduplicated independently. A refund of a purchase this
// ledger never recorded is acknowledged and dropped rather than retried.
func (rt *Router) reversePurchase(r *http.Request, ev pay.Event) int {
	if rt.credits == nil {
		return http.StatusOK
	}
	if ev.Ref == "" || ev.ReversalRef == "" {
		slog.WarnContext(r.Context(), "stripe webhook: reversal without a purchase or a reversal id", "ref", ev.Ref, "reversal", ev.ReversalRef)
		return http.StatusOK
	}
	orig, ok, err := rt.credits.EntryByExternalRef(r.Context(), ev.Ref)
	if err != nil {
		slog.ErrorContext(r.Context(), "stripe webhook: looking up the purchase", "ref", ev.Ref, "error", err)
		return http.StatusInternalServerError
	}
	email, isWallet := credit.WalletEmail(orig.Holder)
	if !ok || !isWallet || orig.Amount <= 0 {
		slog.WarnContext(r.Context(), "stripe webhook: refund for an unknown or unreversable purchase", "ref", ev.Ref, "reversal", ev.ReversalRef)
		return http.StatusOK
	}
	// The balance is read before the reversal to detect whether it makes the
	// balance negative, so the freeze e-mail is sent once, when the balance
	// crosses zero, and not again on a further refund of an already negative
	// wallet.
	before, _ := rt.credits.Balance(r.Context(), orig.Holder)
	var took int64
	switch ev.Kind {
	case pay.KindDisputed:
		took, err = rt.credits.RefundRemainder(r.Context(), ev.Ref, ev.ReversalRef)
	default:
		refunded, charged, usable := chargeTotals(ev.Raw)
		if !usable {
			// Taking back the whole credit for a partial refund is the harm this
			// path exists to prevent, so with no amounts nothing is taken back.
			slog.ErrorContext(r.Context(), "stripe webhook: refund carries no usable amounts, nothing reversed", "ref", ev.Ref, "reversal", ev.ReversalRef)
			return http.StatusOK
		}
		took, err = rt.credits.RefundShare(r.Context(), ev.Ref, ev.ReversalRef, refunded, charged)
	}
	if errors.Is(err, credit.ErrUnknownPurchase) {
		slog.WarnContext(r.Context(), "stripe webhook: refund for an unknown or unreversable purchase", "ref", ev.Ref, "reversal", ev.ReversalRef)
		return http.StatusOK
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "stripe webhook: refund not recorded", "email", email, "error", err)
		return http.StatusInternalServerError
	}
	if took > 0 && before >= 0 && rt.notifier != nil {
		if after, err := rt.credits.Balance(r.Context(), orig.Holder); err == nil && after < 0 {
			if err := rt.notifier.AccountFrozen(r.Context(), email); err != nil {
				notify.LogFailure("account_frozen", email, err)
			}
		}
	}
	return http.StatusOK
}

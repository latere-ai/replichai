package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
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
// buying. The credited amount — gross less the current spread — is computed here
// and carried on the session, so the webhook posts it without recomputing and an
// admin editing the spread mid-flight cannot change what this purchase credits
// (022b). EUR presentment is the processor's Adaptive Pricing, not this handler.
func (rt *Router) startCheckout(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r)
	var body struct {
		AmountMicro int64 `json:"amount_micro"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	// Snap to whole cents (the processor's unit) and price from the policy in
	// force. A failed read resolves to the seeded defaults inside the store.
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
		log.Printf("checkout: create session for %s: %v", id.Email, err)
		writeError(w, http.StatusBadGateway, "checkout_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": out.URL})
}

// stripeWebhook is the one thing that credits a purchase (022b). It is off the
// operator gate because the processor carries no session; the signature is its
// authentication, and the credited amount comes from the verified event's
// metadata, never a redirect parameter. Idempotent by the ledger: a retried
// delivery posts the same external reference and moves the balance once.
func (rt *Router) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if rt.payments == nil {
		w.WriteHeader(http.StatusOK) // nothing configured; acknowledge and drop
		return
	}
	body, err := io.ReadAll(r.Body)
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
			writeError(w, code, "topup_failed") // let Stripe retry
			return
		}
	case pay.KindRefunded, pay.KindDisputed:
		if code := rt.reversePurchase(r, ev); code != http.StatusOK {
			writeError(w, code, "refund_failed") // let Stripe retry
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// creditPurchase posts the wallet credit a paid checkout earned, from the
// verified event's metadata. Returns 200 when there is nothing to post (so a
// malformed event is acknowledged, not retried) or when the post succeeds, and
// 500 only on a store error worth retrying.
func (rt *Router) creditPurchase(r *http.Request, ev pay.Event) int {
	credited, perr := strconv.ParseInt(ev.Meta["credited_micro"], 10, 64)
	if ev.Email == "" || perr != nil || credited <= 0 {
		log.Printf("stripe webhook: unusable checkout metadata (email=%q credited=%q)", ev.Email, ev.Meta["credited_micro"])
		return http.StatusOK
	}
	if rt.credits == nil {
		return http.StatusOK
	}
	if err := rt.credits.TopupPaid(r.Context(), ev.Email, credited, ev.Ref, "stripe:"+ev.Ref); err != nil {
		log.Printf("stripe webhook: topup for %s: %v", ev.Email, err)
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// reversePurchase claws back a refunded or disputed purchase: a negative adjust
// of the *exact micro-USD the purchase credited* (the FX-drift rule — the USD
// credited, not the euros paid), keyed on the reversal's own id so it dedupes
// independently. A refund of a purchase this ledger never recorded is
// acknowledged and dropped, not retried.
func (rt *Router) reversePurchase(r *http.Request, ev pay.Event) int {
	if rt.credits == nil {
		return http.StatusOK
	}
	orig, ok, err := rt.credits.EntryByExternalRef(r.Context(), ev.Ref)
	if err != nil {
		log.Printf("stripe webhook: look up purchase %q: %v", ev.Ref, err)
		return http.StatusInternalServerError
	}
	email, isWallet := credit.WalletEmail(orig.Holder)
	if !ok || !isWallet || orig.Amount <= 0 || ev.ReversalRef == "" {
		log.Printf("stripe webhook: refund for an unknown or unreversable purchase (pi=%q ref=%q)", ev.Ref, ev.ReversalRef)
		return http.StatusOK
	}
	// Whether this reversal is the one that puts the account in the red: read the
	// balance before, so the freeze email is sent once, on the crossing, and not
	// again on a further refund of an already-negative wallet.
	before, _ := rt.credits.Balance(r.Context(), orig.Holder)
	if err := rt.credits.RefundPaid(r.Context(), email, orig.Amount, ev.ReversalRef, "stripe:refund:"+ev.ReversalRef); err != nil {
		log.Printf("stripe webhook: refund for %s: %v", email, err)
		return http.StatusInternalServerError
	}
	if before >= 0 && rt.notifier != nil {
		if after, err := rt.credits.Balance(r.Context(), orig.Holder); err == nil && after < 0 {
			if err := rt.notifier.AccountFrozen(r.Context(), email); err != nil {
				notify.LogFailure("account_frozen", email, err)
			}
		}
	}
	return http.StatusOK
}

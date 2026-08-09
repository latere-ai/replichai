package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/latere-ai/replichai/internal/payment"
	"github.com/latere-ai/replichai/internal/platform"
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
	url, err := rt.payments.CreateCheckout(r.Context(), payment.CheckoutParams{
		Email:       id.Email,
		AmountMinor: cents,
		Currency:    payment.USD,
		SuccessURL:  rt.baseURL + "/wallet?checkout=success",
		CancelURL:   rt.baseURL + "/wallet",
		Meta:        map[string]string{"credited_micro": strconv.FormatInt(credited, 10)},
	})
	if errors.Is(err, payment.ErrUnconfigured) {
		writeError(w, http.StatusNotImplemented, "payments_unconfigured")
		return
	}
	if err != nil {
		log.Printf("checkout: create session for %s: %v", id.Email, err)
		writeError(w, http.StatusBadGateway, "checkout_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
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
	ev, err := rt.payments.ParseWebhook(body, r.Header.Get("Stripe-Signature"))
	if errors.Is(err, payment.ErrUnconfigured) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_signature")
		return
	}
	if ev.Kind == payment.EventCheckoutCompleted {
		credited, perr := strconv.ParseInt(ev.Meta["credited_micro"], 10, 64)
		if ev.Email == "" || perr != nil || credited <= 0 {
			log.Printf("stripe webhook: unusable checkout metadata (email=%q credited=%q)", ev.Email, ev.Meta["credited_micro"])
			w.WriteHeader(http.StatusOK) // nothing to post; do not make Stripe retry
			return
		}
		if rt.credits != nil {
			if err := rt.credits.TopupPaid(r.Context(), ev.Email, credited, ev.PaymentIntent, "stripe:"+ev.PaymentIntent); err != nil {
				log.Printf("stripe webhook: topup for %s: %v", ev.Email, err)
				writeError(w, http.StatusInternalServerError, "topup_failed") // let Stripe retry
				return
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

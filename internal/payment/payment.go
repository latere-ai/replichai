// Package payment is the seam between the app and a card processor. It says what
// a top-up purchase needs — open a hosted checkout, verify and read a webhook —
// without naming the vendor, so the orchestration is testable offline and a
// Stripe adapter is one implementation rather than the only shape money can take
// (022a). The real adapter and the HTTP that drives it land in 022b; this
// package is the port and a memory fake.
package payment

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrUnconfigured reports a payment operation on a deployment with no processor
// keys. It is not a failure: a local run and every test that is not about
// payment want an app that boots and refuses to sell rather than one that
// panics — the same shape as a nil rate card meaning "unpriced" (011).
var ErrUnconfigured = errors.New("payments are not configured")

// ErrBadSignature reports a webhook whose signature did not verify. A delivery
// that cannot be authenticated posts nothing (022b).
var ErrBadSignature = errors.New("webhook signature does not verify")

// Currency is a processor presentment currency. The ledger is micro-USD end to
// end (011); a non-USD charge is converted once, by the processor, at the moment
// of purchase (022). The allow-list is deliberately small.
type Currency string

// The currencies a top-up may be charged in.
const (
	USD Currency = "usd"
	EUR Currency = "eur"
)

// CheckoutParams describe a top-up a person is about to pay for. Meta rides on
// the session so the webhook can credit correctly — the snapshotted spread, the
// currency — the way 011 snapshots a rate card onto a job at enqueue.
type CheckoutParams struct {
	Email       string
	AmountMinor int64 // smallest unit of Currency (e.g. cents), the processor's unit
	Currency    Currency
	SuccessURL  string
	CancelURL   string
	Meta        map[string]string
}

// EventKind is the subset of processor webhook events this product acts on.
type EventKind string

// The webhook events the ledger reacts to.
const (
	// EventCheckoutCompleted is a paid top-up: credit the wallet (022b).
	EventCheckoutCompleted EventKind = "checkout.session.completed"
	// EventChargeRefunded and EventDisputeCreated are money clawed back: reverse
	// the credit (022c).
	EventChargeRefunded EventKind = "charge.refunded"
	EventDisputeCreated EventKind = "charge.dispute.created"
)

// Event is a verified webhook, already reduced to what the ledger needs. The
// adapter does the vendor-specific work — the signature check, and following a
// charge to its balance transaction for the USD a EUR charge is worth — so the
// handler sees one flat, trustworthy shape and never a redirect parameter.
type Event struct {
	Kind          EventKind
	Email         string
	PaymentIntent string
	// GrossUSDMicro is what the charge is worth in micro-USD, at the processor's
	// own rate on this charge. It is the figure the spread is applied to (022b).
	GrossUSDMicro int64
	// RefundID identifies a refund or dispute, distinct from PaymentIntent, so a
	// reversal dedupes on its own reference (022c).
	RefundID string
	// Meta is the session metadata echoed back, including the snapshotted spread.
	Meta map[string]string
}

// Provider is the port. CreateCheckout opens a hosted payment page and returns
// its URL; ParseWebhook verifies a delivery's signature and reduces it to an
// Event. An unconfigured deployment's Provider returns ErrUnconfigured.
type Provider interface {
	CreateCheckout(ctx context.Context, p CheckoutParams) (url string, err error)
	ParseWebhook(payload []byte, sigHeader string) (Event, error)
}

// MemProvider is a memory fake of a Provider for offline tests: CreateCheckout
// records the params and hands back a canned URL, and ParseWebhook accepts a
// JSON-encoded Event when the signature header matches — enough for the checkout
// and webhook handlers (022b/022c) to be tested with no Stripe.
type MemProvider struct {
	// URL is returned by every CreateCheckout; empty means a default is used.
	URL string
	// Secret is the signature ParseWebhook requires; empty means accept any.
	Secret string
	// Calls records every checkout created, for assertions.
	Calls []CheckoutParams
}

// CreateCheckout records the params and returns the canned URL.
func (m *MemProvider) CreateCheckout(_ context.Context, p CheckoutParams) (string, error) {
	m.Calls = append(m.Calls, p)
	if m.URL != "" {
		return m.URL, nil
	}
	return "https://pay.test/checkout", nil
}

// ParseWebhook verifies the signature against Secret and decodes payload as an
// Event, so a test synthesizes the deliveries the real adapter would.
func (m *MemProvider) ParseWebhook(payload []byte, sigHeader string) (Event, error) {
	if m.Secret != "" && sigHeader != m.Secret {
		return Event{}, ErrBadSignature
	}
	var e Event
	if err := json.Unmarshal(payload, &e); err != nil {
		return Event{}, err
	}
	return e, nil
}

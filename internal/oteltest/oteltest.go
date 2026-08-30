// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package oteltest installs a recording tracer provider so a test can assert
// what instrumentation does rather than what type it is.
//
// A type assertion on a client's Transport is not evidence. It holds in a
// process where no TracerProvider is registered at all, where no span is ever
// started and no traceparent is ever injected, which is exactly the state a
// client that is wrapped but inert is in. What the recorder here holds is the
// spans that were actually produced, so that client fails.
package oteltest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// Record registers a tracer provider that keeps every finished span in memory,
// together with the W3C propagators the instrumentation reads and writes. Both
// are process-global, so the previous values are restored when the test ends.
//
// Sampling is unconditional. The library's default ratio drops four spans in
// five, which would make every assertion below a coin toss.
func Record(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	prevProvider := otel.GetTracerProvider()
	prevPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
		otel.SetTextMapPropagator(prevPropagator)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("oteltest: shutting the recording provider down: %v", err)
		}
	})
	return recorder
}

// SpansOfKind returns the finished spans of one kind, in the order they ended.
func SpansOfKind(recorder *tracetest.SpanRecorder, kind trace.SpanKind) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.SpanKind() == kind {
			out = append(out, span)
		}
	}
	return out
}

// Peer is a test server that remembers the trace context header of every
// request it served. The header is the other half of a client span: a span the
// caller records but does not propagate leaves the far side unable to join the
// trace, and only the receiving end can tell the difference.
type Peer struct {
	*httptest.Server

	mu          sync.Mutex
	traceparent string
}

// NewPeer starts a server that records the traceparent of each request and then
// delegates to h. It is closed when the test ends.
func NewPeer(t *testing.T, h http.HandlerFunc) *Peer {
	t.Helper()
	p := &Peer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.traceparent = r.Header.Get("traceparent")
		p.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(p.Close)
	return p
}

// Traceparent is the header of the most recent request the peer served, empty
// when the caller sent none.
func (p *Peer) Traceparent() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.traceparent
}

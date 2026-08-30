// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	xotel "latere.ai/x/pkg/otel"

	"github.com/latere-ai/replichai/internal/oteltest"
)

// The server surface has to record a span for every request it serves, and that
// span has to be the caller's when the caller sent one. A trace that restarts
// at this boundary is the failure the whole exercise is about.
func TestServerRecordsAServerSpanAndJoinsTheCallersTrace(t *testing.T) {
	recorder := oteltest.Record(t)

	served := false
	srv := newServer(":0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusNoContent)
	}))
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A caller's trace, sent the way any instrumented client would send it.
	const caller = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req.Header.Set("traceparent", caller)

	// http.DefaultClient on purpose: it is not instrumented, so the only span
	// this test can record is the server's own.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requesting the server: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if !served {
		t.Fatal("the wrapped handler never ran")
	}
	if got := resp.Header.Get(xotel.TraceHeaderName); got == "" {
		t.Errorf("no %s response header; the middleware saw no valid span", xotel.TraceHeaderName)
	}

	spans := oteltest.SpansOfKind(recorder, trace.SpanKindServer)
	if len(spans) != 1 {
		t.Fatalf("server spans recorded = %d, want 1", len(spans))
	}
	if got := spans[0].SpanContext().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("server span trace id = %s, want the caller's 4bf92f3577b34da6a3ce929d0e0e4736", got)
	}
}

// The paper resolver reaches arbitrary publisher hosts. It is still a hop the
// trace has to survive, so its client carries the context out.
func TestPaperFetcherRecordsAClientSpanAndPropagates(t *testing.T) {
	recorder := oteltest.Record(t)
	peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("paper"))
	})

	body, err := newHTTPFetcher(5*time.Second).Fetch(context.Background(), peer.URL)
	if err != nil {
		t.Fatalf("fetching: %v", err)
	}
	if string(body) != "paper" {
		t.Fatalf("body = %q, want %q", body, "paper")
	}

	if len(oteltest.SpansOfKind(recorder, trace.SpanKindClient)) != 1 {
		t.Errorf("client spans recorded = %d, want 1", len(oteltest.SpansOfKind(recorder, trace.SpanKindClient)))
	}
	if peer.Traceparent() == "" {
		t.Error("the peer received no traceparent; the hop starts a new root trace")
	}
}

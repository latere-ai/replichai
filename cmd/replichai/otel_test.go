// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/latere-ai/replichai/internal/oteltest"
)

// TestCLIPaperFetcherRecordsAClientSpanAndPropagates checks that a paper fetch
// records a client span and propagates the trace context. A trace for anything
// a person runs by hand starts in the terminal. A resolver call that carries no
// context would make the deployment's side of that work a separate trace,
// unattached to the command that caused it.
func TestCLIPaperFetcherRecordsAClientSpanAndPropagates(t *testing.T) {
	recorder := oteltest.Record(t)
	peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("paper"))
	})

	body, err := httpFetcher{timeout: 5 * time.Second, private: true}.Fetch(context.Background(), peer.URL)
	if err != nil {
		t.Fatalf("fetching: %v", err)
	}
	if string(body) != "paper" {
		t.Fatalf("body = %q, want %q", body, "paper")
	}

	if got := len(oteltest.SpansOfKind(recorder, trace.SpanKindClient)); got != 1 {
		t.Errorf("client spans recorded = %d, want 1", got)
	}
	if peer.Traceparent() == "" {
		t.Error("the peer received no traceparent; the hop starts a new root trace")
	}
}

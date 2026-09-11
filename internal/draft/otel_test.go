// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package draft

import (
	"io"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/latere-ai/replichai/internal/oteltest"
)

// Drafting calls a model vendor while a person waits on the request, and the
// call is the slowest step in that route. The default client New builds when
// a caller passes none must be instrumented, or that wait is not traced.
func TestDefaultModelClientIsInstrumented(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		client   func(Model) *http.Client
	}{
		{"anthropic", "anthropic", func(m Model) *http.Client { return m.(*anthropicModel).client }},
		{"openai", "openai", func(m Model) *http.Client { return m.(*openAIModel).client }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := oteltest.Record(t)
			peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{}`))
			})

			model, err := New(Config{Provider: tc.provider, Model: "a-model"}, "key", nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			resp, err := tc.client(model).Get(peer.URL)
			if err != nil {
				t.Fatalf("calling the peer: %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			if err := resp.Body.Close(); err != nil {
				t.Fatalf("closing the body: %v", err)
			}

			if got := len(oteltest.SpansOfKind(recorder, trace.SpanKindClient)); got != 1 {
				t.Errorf("client spans recorded = %d, want 1", got)
			}
			if peer.Traceparent() == "" {
				t.Error("the peer received no traceparent; the hop starts a new root trace")
			}
		})
	}
}

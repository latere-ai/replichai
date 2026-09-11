// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package cloud

import (
	"io"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/latere-ai/replichai/internal/oteltest"
)

// A session and an API client are the two ways the CLI reaches the platform.
// Both once used http.DefaultClient, which traces nothing, so a run started
// from a user's machine arrived at the deployment with no parent trace.
func TestCloudConstructorsInstrumentTheirClient(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, base string) *http.Client
	}{
		{"New", func(t *testing.T, base string) *http.Client {
			return New(t.TempDir(), base).HTTP
		}},
		{"NewClient", func(t *testing.T, base string) *http.Client {
			return NewClient(New(t.TempDir(), base), base).HTTP
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := oteltest.Record(t)
			peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{}`))
			})

			resp, err := tc.build(t, peer.URL).Get(peer.URL)
			if err != nil {
				t.Fatalf("calling the peer: %v", err)
			}
			// The client span ends when the response body is closed. The
			// instrumented transport wraps the body so the span covers the
			// whole exchange.
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

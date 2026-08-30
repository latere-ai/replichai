// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package github

import (
	"io"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/latere-ai/replichai/internal/oteltest"
)

// Publishing a reproduction is the longest hop the server makes, and the one
// most worth seeing in a trace when it is slow. Both constructors have to build
// an instrumented client; NewApp used to be a second copy of the same literal,
// which is exactly how one of them goes uninstrumented unnoticed.
func TestClientConstructorsInstrumentTheirTransport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, base string) *Client
	}{
		{"New", func(_ *testing.T, base string) *Client {
			return New("token", WithBaseURL(base))
		}},
		{"NewApp", func(t *testing.T, base string) *Client {
			return appClientForTest(t, base, time.Now())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := oteltest.Record(t)
			peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[]`))
			})

			resp, err := tc.build(t, peer.URL).http.Get(peer.URL)
			if err != nil {
				t.Fatalf("calling the peer: %v", err)
			}
			// The client span ends when the response body does: the
			// instrumented transport wraps the body so the span covers the
			// whole exchange, not just the headers.
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

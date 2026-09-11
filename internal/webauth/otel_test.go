// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/latere-ai/replichai/internal/oteltest"
)

// assertTraced checks the two facts that together show a hop is instrumented:
// this side recorded a client span, and the other side received the trace it
// belongs to. Either alone is satisfied by a client that is wrapped but inert,
// or by one that traces locally and propagates nothing.
func assertTraced(t *testing.T, recorder *tracetest.SpanRecorder, peer *oteltest.Peer) {
	t.Helper()
	if got := len(oteltest.SpansOfKind(recorder, trace.SpanKindClient)); got != 1 {
		t.Errorf("client spans recorded = %d, want 1", got)
	}
	if peer.Traceparent() == "" {
		t.Error("the peer received no traceparent; the hop starts a new root trace")
	}
}

// Discovery and userinfo are on the sign-in path, where a slow identity
// provider is indistinguishable from a slow application unless both hops are in
// the same trace.
func TestOIDCCallsRecordClientSpansAndPropagate(t *testing.T) {
	t.Run("Discover", func(t *testing.T) {
		recorder := oteltest.Record(t)
		peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"userinfo_endpoint":"https://idp.example/userinfo"}`))
		})

		eps, err := Discover(t.Context(), peer.URL)
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if eps.UserInfo != "https://idp.example/userinfo" {
			t.Fatalf("userinfo endpoint = %q", eps.UserInfo)
		}
		assertTraced(t, recorder, peer)
	})

	t.Run("HTTPUserInfo", func(t *testing.T) {
		recorder := oteltest.Record(t)
		peer := oteltest.NewPeer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"email":"a@example.com"}`))
		})

		profile, err := HTTPUserInfo(peer.URL)(t.Context(), "access-token")
		if err != nil {
			t.Fatalf("HTTPUserInfo: %v", err)
		}
		if profile.Email != "a@example.com" {
			t.Fatalf("email = %q", profile.Email)
		}
		assertTraced(t, recorder, peer)
	})
}

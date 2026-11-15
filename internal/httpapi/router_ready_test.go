// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// TestReadyzPingsTheDatabaseAndHealthzDoesNot checks that readiness follows the
// database while liveness stays a plain answer, so a database outage takes a pod
// out of the Service without restarting it.
func TestReadyzPingsTheDatabaseAndHealthzDoesNot(t *testing.T) {
	var down error
	h := NewRouter(Deps{Gate: allowAll, Ready: func(context.Context) error { return down }}).Handler()

	if w := do(t, h, "GET", "/readyz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("readyz with the database up = %d", w.Code)
	}
	down = errors.New("connection refused")
	if w := do(t, h, "GET", "/readyz", "", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with the database down = %d, want 503", w.Code)
	}
	if w := do(t, h, "GET", "/healthz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("healthz with the database down = %d, want 200", w.Code)
	}
}

func TestReadyzWithNoDatabaseIsReady(t *testing.T) {
	h := NewRouter(Deps{Gate: allowAll}).Handler()
	if w := do(t, h, "GET", "/readyz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("readyz with no ping = %d", w.Code)
	}
}

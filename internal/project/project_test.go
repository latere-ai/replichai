// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"testing"
	"time"
)

// TestRedactedWithholdsTheWithdrawalReason checks that a stranger reading a
// republished project does not receive why its owner once withdrew it. The
// record stays on the row as history; the reason is the owner's own note.
func TestRedactedWithholdsTheWithdrawalReason(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	p := Project{
		Owner: "owner-sub", Visibility: VisibilityPublic,
		WithdrawnAt: &at, WithdrawnBy: "owner-sub", WithdrawnReason: "unreleased figure",
	}
	got := p.Redacted()
	if got.WithdrawnReason != "" || got.WithdrawnBy != "" {
		t.Errorf("a stranger's copy carries the withdrawal note: %+v", got)
	}
	if got.WithdrawnAt == nil || !got.WithdrawnAt.Equal(at) {
		t.Errorf("a stranger's copy lost that the project was withdrawn: %+v", got)
	}
}

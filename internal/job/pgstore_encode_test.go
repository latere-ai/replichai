package job

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// TestEncodeStatusRejectsMalformedSessionRef pins the write half of the
// database boundary: SessionRef is raw JSON the caller supplies, and a run
// status carrying invalid bytes must fail the write rather than encode to nil
// and null the column that holds the session's usage.
func TestEncodeStatusRejectsMalformedSessionRef(t *testing.T) {
	status := provider.RunStatus{
		State:      provider.StateRunning,
		SessionRef: json.RawMessage(`{"runtime":`),
		Usage:      provider.Usage{InputTokens: 42},
	}

	raw, err := encodeStatus(status)
	if err == nil || !strings.Contains(err.Error(), "encode status") {
		t.Fatalf("encodeStatus error = %v, want an encode-status error", err)
	}
	if raw != nil {
		t.Fatalf("encodeStatus bytes = %q, want nil", raw)
	}
}

// TestEncodeStatusKeepsUsage is the companion pass: a well-formed status still
// round-trips, so the error path above is not simply refusing everything.
func TestEncodeStatusKeepsUsage(t *testing.T) {
	raw, err := encodeStatus(provider.RunStatus{State: provider.StateDone, Usage: provider.Usage{OutputTokens: 7}})
	if err != nil {
		t.Fatalf("encodeStatus: %v", err)
	}
	var back provider.RunStatus
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Usage.OutputTokens != 7 || back.State != provider.StateDone {
		t.Fatalf("round-trip = %+v", back)
	}
}

// TestEncodePhaseAtRejectsUnencodableTime covers the other written column. The
// clock is injectable, so a timestamp time.Time refuses to encode must fail the
// write instead of erasing the whole timeline.
func TestEncodePhaseAtRejectsUnencodableTime(t *testing.T) {
	outOfRange := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)

	raw, err := encodePhaseAt(map[Phase]time.Time{PhaseSubmitted: outOfRange})
	if err == nil || !strings.Contains(err.Error(), "encode phase_at") {
		t.Fatalf("encodePhaseAt error = %v, want an encode-phase_at error", err)
	}
	if raw != nil {
		t.Fatalf("encodePhaseAt bytes = %q, want nil", raw)
	}
}

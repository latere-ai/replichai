package publish

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

func TestRenderTranscript(t *testing.T) {
	events := []provider.Event{
		{Role: "agent", Kind: "message", Content: "Reproducing the inner loop.",
			Time: time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
			Raw:  json.RawMessage(`{"type":"agent.message","text":"Reproducing the inner loop."}`)},
		{Role: "agent", Kind: "tool_use", Content: ""},
	}
	jsonl, md := RenderTranscript(events)

	// jsonl: one line per event, the raw preserved.
	lines := strings.Split(strings.TrimSpace(string(jsonl)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 jsonl lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"type":"agent.message"`) {
		t.Errorf("jsonl did not preserve the raw event: %s", lines[0])
	}
	// A line with no raw falls back to the neutral event.
	if !strings.Contains(lines[1], `"Kind":"tool_use"`) {
		t.Errorf("jsonl line without raw should marshal the event: %s", lines[1])
	}

	// md: human narrative with role/kind and content.
	if !strings.Contains(string(md), "agent · message") || !strings.Contains(string(md), "Reproducing the inner loop.") {
		t.Errorf("md missing the message:\n%s", md)
	}
}

func TestTranscriptLeaksFlagsSecrets(t *testing.T) {
	events := []provider.Event{
		{Role: "agent", Kind: "message", Content: "installed the dependency"},
		{Role: "agent", Kind: "tool_result", Content: "export TOKEN=AKIAIOSFODNN7EXAMPLE"},
	}
	leaks := TranscriptLeaks(events)
	if len(leaks) != 1 || leaks[0].Index != 1 {
		t.Fatalf("want the secret-bearing event flagged, got %+v", leaks)
	}
	if leaks[0].Reason == "" {
		t.Error("a leak needs a reason")
	}
}

func TestTranscriptLeaksCleanTranscript(t *testing.T) {
	events := []provider.Event{
		{Role: "agent", Kind: "message", Content: "reproduced the figure"},
		{Role: "agent", Kind: "thinking", Content: "the rate is 3/5"},
	}
	if leaks := TranscriptLeaks(events); len(leaks) != 0 {
		t.Errorf("a clean transcript should not be flagged, got %+v", leaks)
	}
}

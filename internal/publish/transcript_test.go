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
			Raw:  json.RawMessage(`{"type":"agent.message","content":[{"type":"text","text":"Reproducing the inner loop."}]}`)},
		{Role: "agent", Kind: "tool_use", Tool: "bash", Content: `{"command":"ls -la"}`},
		{Role: "agent", Kind: "tool_result", Content: "main.py\ndata/"},
		{Role: "agent", Kind: "thinking"},
	}
	jsonl, md := RenderTranscript(events)

	// jsonl: one normalised event per line — typed fields up top, raw nested.
	lines := strings.Split(strings.TrimSpace(string(jsonl)), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 jsonl lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"role":"agent"`) || !strings.Contains(lines[0], `"kind":"message"`) {
		t.Errorf("jsonl not normalised: %s", lines[0])
	}
	if !strings.Contains(lines[0], `"type":"agent.message"`) {
		t.Errorf("jsonl did not nest the raw event: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"tool":"bash"`) {
		t.Errorf("jsonl tool_use missing the tool: %s", lines[1])
	}

	s := string(md)
	for _, want := range []string{
		"<details open>",                     // message reads inline, in a details block
		"Reproducing the inner loop.",        // message text
		"<summary>agent · tool_use · `bash`", // tool in the summary, not a heading
		`"command":"ls -la"`,                 // tool input
		"main.py",                            // tool result output
		"reasoned here",                      // thinking marker
	} {
		if !strings.Contains(s, want) {
			t.Errorf("md missing %q:\n%s", want, s)
		}
	}
	// No markdown heading is used for event boundaries — that is the whole point.
	if strings.Contains(s, "\n### ") {
		t.Errorf("event boundaries must not be markdown headings:\n%s", s)
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

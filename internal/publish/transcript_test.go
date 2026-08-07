package publish

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

func TestRenderTranscriptFiles(t *testing.T) {
	events := []provider.Event{
		{Role: "agent", Kind: "message", Content: "Reproducing the inner loop.",
			Time: time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
			Raw:  json.RawMessage(`{"type":"agent.message","content":[{"type":"text","text":"Reproducing the inner loop."}]}`)},
		{Role: "agent", Kind: "tool_use", Tool: "bash", Content: `{"command":"ls -la"}`},
		{Role: "agent", Kind: "tool_result", Content: "main.py\ndata/"},
		{Role: "agent", Kind: "thinking"},
	}
	files, err := RenderTranscriptFiles(events)
	if err != nil {
		t.Fatalf("RenderTranscriptFiles: %v", err)
	}

	// The expected file set: the machine stream, the index, and one chunk.
	for _, want := range []string{TranscriptJSONL, TranscriptMD, transcriptDir + "/001.md"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing file %q; have %v", want, keys(files))
		}
	}

	// jsonl: one normalised event per line — typed fields up top, raw nested.
	lines := strings.Split(strings.TrimSpace(string(files[TranscriptJSONL])), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 jsonl lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"role":"agent"`) || !strings.Contains(lines[0], `"type":"agent.message"`) {
		t.Errorf("jsonl not normalised + nested: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"tool":"bash"`) {
		t.Errorf("jsonl tool_use missing the tool: %s", lines[1])
	}

	// The index links the chunk.
	if !strings.Contains(string(files[TranscriptMD]), transcriptDir+"/001.md") {
		t.Errorf("index does not link the chunk:\n%s", files[TranscriptMD])
	}

	// The chunk: details blocks, per-kind rendering, no colliding headings.
	chunk := string(files[transcriptDir+"/001.md"])
	for _, want := range []string{
		"<details open>", "Reproducing the inner loop.",
		"<summary>agent · tool_use · `bash`", `"command":"ls -la"`,
		"main.py", "reasoned here",
	} {
		if !strings.Contains(chunk, want) {
			t.Errorf("chunk missing %q:\n%s", want, chunk)
		}
	}
	if strings.Contains(chunk, "\n### ") {
		t.Errorf("event boundaries must not be markdown headings:\n%s", chunk)
	}
}

func TestRenderTranscriptChunksAndInterlinks(t *testing.T) {
	events := make([]provider.Event, 45) // > eventsPerChunk, so two parts
	for i := range events {
		events[i] = provider.Event{Role: "agent", Kind: "message", Content: "step"}
	}
	files, err := RenderTranscriptFiles(events)
	if err != nil {
		t.Fatalf("RenderTranscriptFiles: %v", err)
	}

	if _, ok := files[transcriptDir+"/002.md"]; !ok {
		t.Fatalf("45 events should span two chunks; have %v", keys(files))
	}
	// The first chunk links to the next and the index; the second links back.
	if !strings.Contains(string(files[transcriptDir+"/001.md"]), "002.md") {
		t.Error("part 001 should link to part 002")
	}
	if !strings.Contains(string(files[transcriptDir+"/002.md"]), "001.md") {
		t.Error("part 002 should link back to part 001")
	}
	if !strings.Contains(string(files[transcriptDir+"/001.md"]), "../"+TranscriptMD) {
		t.Error("a chunk should link up to the index")
	}
}

func TestRenderTranscriptRejectsAnUnencodableEvent(t *testing.T) {
	_, err := RenderTranscriptFiles([]provider.Event{
		{Kind: "message", Content: "valid"},
		{Kind: "message", Content: "invalid raw", Raw: json.RawMessage(`{`)},
	})
	if err == nil || !strings.Contains(err.Error(), "event 2") {
		t.Fatalf("RenderTranscriptFiles error = %v, want event index", err)
	}
}

func keys(m map[string][]byte) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
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

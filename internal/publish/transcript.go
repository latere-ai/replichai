package publish

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// Transcript file names committed into a reproduction repository.
const (
	TranscriptJSONL = "transcript.jsonl"
	TranscriptMD    = "TRANSCRIPT.md"
)

// RenderTranscript renders a run's events two ways: transcript.jsonl, the
// canonical machine-readable stream — one normalised event per line, with the
// typed fields (role, kind, tool, content) up top and the backend's original
// event nested under `raw`, so it is both consistent to parse and lossless —
// and TRANSCRIPT.md, a human-readable narrative rendered per kind.
func RenderTranscript(events []provider.Event) (jsonl, md []byte) {
	var j, m bytes.Buffer
	m.WriteString("# Reproduction transcript\n\n")
	m.WriteString("The agent's full session, as recorded by the backend — the evidence behind the verdict. The canonical machine-readable form is `transcript.jsonl`; this file is the narrative.\n")

	for _, e := range events {
		if line, err := json.Marshal(e); err == nil {
			j.Write(line)
			j.WriteByte('\n')
		}
		renderEventMD(&m, e)
	}
	return j.Bytes(), m.Bytes()
}

// renderEventMD writes one event as a markdown block, shaped to its kind:
// a tool call shows its name and input, a tool result its output, a message its
// text, and thinking a marker (the backend exposes no thinking content).
func renderEventMD(m *bytes.Buffer, e provider.Event) {
	stamp := ""
	if !e.Time.IsZero() {
		stamp = " · " + e.Time.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(m, "\n### %s · %s%s\n\n", e.Role, e.Kind, stamp)

	switch {
	case e.Tool != "":
		fmt.Fprintf(m, "call `%s`\n", e.Tool)
		if e.Content != "" {
			fmt.Fprintf(m, "\n```json\n%s\n```\n", e.Content)
		}
	case e.Kind == "thinking":
		m.WriteString("_(the agent reasoned here; the backend does not expose the content)_\n")
	case e.Kind == "tool_result":
		if e.Content != "" {
			fmt.Fprintf(m, "```\n%s\n```\n", e.Content)
		} else {
			m.WriteString("_(no output)_\n")
		}
	case e.Content != "":
		m.WriteString(e.Content)
		m.WriteString("\n")
	}
}

// TranscriptLeak is one event whose content carries a redistribution risk.
type TranscriptLeak struct {
	Index  int
	Reason string
}

// TranscriptLeaks flags events whose content carries a high-signal secret or
// credential — the concrete, detectable leak risk in a transcript. It is a
// backstop, not an exhaustive third-party-content check: detecting an embedded
// dataset or a paper's copyrighted bulk text in freeform reasoning is not
// reliable structurally, so a transcript stays private by default and going
// public is a human decision layered on top of this. A non-empty result bars
// the repository from the public flip.
func TranscriptLeaks(events []provider.Event) []TranscriptLeak {
	var leaks []TranscriptLeak
	for i, e := range events {
		if reason := secretInContent([]byte(e.Content)); reason != "" {
			leaks = append(leaks, TranscriptLeak{Index: i, Reason: reason})
		}
	}
	return leaks
}

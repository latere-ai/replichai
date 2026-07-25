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
// canonical one-event-per-line stream (each line the backend's original event,
// so nothing is lost), and TRANSCRIPT.md, a human-readable narrative.
func RenderTranscript(events []provider.Event) (jsonl, md []byte) {
	var j, m bytes.Buffer
	m.WriteString("# Reproduction transcript\n\n")
	m.WriteString("The agent's full session, as recorded by the backend — the evidence behind the verdict.\n\n")

	for _, e := range events {
		line := e.Raw
		if len(line) == 0 {
			line, _ = json.Marshal(e)
		}
		j.Write(bytes.TrimSpace(line))
		j.WriteByte('\n')

		stamp := ""
		if !e.Time.IsZero() {
			stamp = e.Time.UTC().Format(time.RFC3339) + " · "
		}
		fmt.Fprintf(&m, "### %s%s · %s\n", stamp, e.Role, e.Kind)
		if e.Content != "" {
			m.WriteString("\n")
			m.WriteString(e.Content)
			m.WriteString("\n")
		}
		m.WriteString("\n")
	}
	return j.Bytes(), m.Bytes()
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

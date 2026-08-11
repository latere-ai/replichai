package publish

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// Transcript file names and layout committed into a reproduction repository.
const (
	TranscriptJSONL = "transcript.jsonl" // the canonical machine-readable stream
	TranscriptMD    = "TRANSCRIPT.md"    // the human-readable index
	transcriptDir   = "transcript"       // the interlinked markdown chunks live here

	eventsPerChunk  = 40      // events per markdown chunk
	maxEventMDBytes = 8 << 10 // per-event content cap in the narrative
)

// RenderTranscriptFiles renders a run's events into the files to publish:
// transcript.jsonl (the canonical machine stream), TRANSCRIPT.md (a navigable
// index), and transcript/NNN.md (interlinked chunks). Chunking keeps any single
// file small enough to read and to commit — a full run's transcript can be many
// megabytes as one file.
func RenderTranscriptFiles(events []provider.Event) (map[string][]byte, error) {
	files := make(map[string][]byte)

	var j bytes.Buffer
	for i, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("encode transcript event %d: %w", i+1, err)
		}
		j.Write(line)
		j.WriteByte('\n')
	}
	files[TranscriptJSONL] = j.Bytes()

	chunks := (len(events) + eventsPerChunk - 1) / eventsPerChunk
	if chunks == 0 {
		chunks = 1 // an empty transcript still gets an index and one (empty) part
	}
	for c := 0; c < chunks; c++ {
		start, end := c*eventsPerChunk, min((c+1)*eventsPerChunk, len(events))
		var m bytes.Buffer
		nav := chunkNav(c, chunks)
		fmt.Fprintf(&m, "%s\n\n**Events %d–%d of %d.**\n", nav, start+1, end, len(events))
		for _, e := range events[start:end] {
			renderEventMD(&m, e)
		}
		fmt.Fprintf(&m, "\n%s\n", nav)
		files[fmt.Sprintf("%s/%03d.md", transcriptDir, c+1)] = m.Bytes()
	}

	files[TranscriptMD] = renderIndex(events, chunks)
	return files, nil
}

// chunkNav is the previous/index/next line at the top and bottom of each chunk.
func chunkNav(c, chunks int) string {
	parts := []string{fmt.Sprintf("[↑ index](../%s)", TranscriptMD)}
	if c > 0 {
		parts = append(parts, fmt.Sprintf("[← part %03d](%03d.md)", c, c))
	}
	if c < chunks-1 {
		parts = append(parts, fmt.Sprintf("[part %03d →](%03d.md)", c+2, c+2))
	}
	return strings.Join(parts, " · ")
}

// renderIndex is TRANSCRIPT.md: a table of contents linking each chunk.
func renderIndex(events []provider.Event, chunks int) []byte {
	var m bytes.Buffer
	m.WriteString("# Reproduction transcript\n\n")
	m.WriteString("The agent's full session, as recorded by the backend — the evidence behind the verdict. The canonical machine-readable form is [`transcript.jsonl`](transcript.jsonl); the parts below are the human-readable narrative.\n\n")
	fmt.Fprintf(&m, "%d events across %d part(s).\n\n", len(events), chunks)
	m.WriteString("| Part | Events | Opens with |\n|---|---|---|\n")
	for c := 0; c < chunks; c++ {
		start, end := c*eventsPerChunk, min((c+1)*eventsPerChunk, len(events))
		fmt.Fprintf(&m, "| [%03d](%s/%03d.md) | %d–%d | %s |\n",
			c+1, transcriptDir, c+1, start+1, end, firstMessageSnippet(events[start:end]))
	}
	return m.Bytes()
}

// firstMessageSnippet is a short, table-safe preview of a chunk's first message.
func firstMessageSnippet(events []provider.Event) string {
	for _, e := range events {
		if e.Kind == "message" && e.Content != "" {
			s := strings.ReplaceAll(e.Content, "\n", " ")
			s = strings.ReplaceAll(s, "|", "\\|")
			if len(s) > 70 {
				s = s[:70] + "…"
			}
			return s
		}
	}
	return "—"
}

// renderEventMD writes one event as a collapsible <details> block. Using a
// <summary> for the boundary rather than a markdown heading is what keeps the
// agent's own markdown — its headings, lists, code fences — from colliding with
// the transcript's structure. Oversized content is capped so one giant tool
// output cannot bloat a chunk.
func renderEventMD(m *bytes.Buffer, e provider.Event) {
	stamp := ""
	if !e.Time.IsZero() {
		stamp = " · " + e.Time.UTC().Format(time.RFC3339)
	}

	if e.Kind == "thinking" {
		fmt.Fprintf(m, "\n_%s · thinking%s — reasoned here; the backend does not expose the content._\n", e.Role, stamp)
		return
	}

	summary := string(e.Role) + " · " + string(e.Kind)
	if e.Tool != "" {
		summary += " · `" + e.Tool + "`"
	}
	summary += stamp

	open := ""
	if e.Kind == "message" {
		open = " open"
	}
	fmt.Fprintf(m, "\n<details%s>\n<summary>%s</summary>\n\n", open, summary)

	content := capContent(e.Content)
	switch {
	case e.Tool != "":
		if content != "" {
			fmt.Fprintf(m, "```json\n%s\n```\n", content)
		} else {
			m.WriteString("_(no input)_\n")
		}
	case e.Kind == "tool_result":
		if content != "" {
			fmt.Fprintf(m, "```\n%s\n```\n", content)
		} else {
			m.WriteString("_(no output)_\n")
		}
	case content != "":
		m.WriteString(content)
		m.WriteString("\n")
	default:
		m.WriteString("_(no content)_\n")
	}
	m.WriteString("\n</details>\n")
}

func capContent(s string) string {
	if len(s) <= maxEventMDBytes {
		return s
	}
	return s[:maxEventMDBytes] + fmt.Sprintf("\n…[%d bytes elided; see transcript.jsonl]", len(s)-maxEventMDBytes)
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

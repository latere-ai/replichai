// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
)

// benchSession is a Session over a bare temporary directory. CreateSession
// also initializes a git workspace, which starts a subprocess; the transcript
// and status files do not need one.
func benchSession(b *testing.B) *Session {
	b.Helper()
	return &Session{dir: b.TempDir(), id: "7f3a91c2"}
}

// benchEvents builds the normalized events one run appends to its transcript:
// agent text, a tool call, its result and a usage-bearing result line, each
// carrying the harness's original stream line in Raw.
func benchEvents(target int) []managed.Event {
	var events []managed.Event
	at := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	size := 0
	for i := 0; size < target; i++ {
		at = at.Add(2 * time.Second)
		text := fmt.Sprintf("Step %d: run the evaluation script on the held-out split with seed %d and compare it with Table 2.", i, i)
		result := strings.Repeat(fmt.Sprintf("epoch %d loss 0.%04d acc 0.8%03d\\n", i, i%9999, i%999), 6)
		batch := []managed.Event{
			{
				ID: fmt.Sprintf("worker-0-%d", 4*i), Role: managed.RoleAgent, Kind: "message", Content: text, Time: at,
				Raw: jsontext.Value(fmt.Sprintf(`{"type":"assistant","message":{"id":"msg_%d","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":%q}]}}`, i, text)),
			},
			{
				ID: fmt.Sprintf("worker-0-%d", 4*i+1), Role: managed.RoleAgent, Kind: "tool_use", Tool: "Bash",
				Content: fmt.Sprintf(`{"command":"python eval.py --split %d --seed %d"}`, i%7, i), Time: at.Add(100 * time.Millisecond),
				Raw: jsontext.Value(fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_%d","name":"Bash","input":{"command":"python eval.py --split %d --seed %d"}}]}}`, i, i%7, i)),
			},
			{
				ID: fmt.Sprintf("worker-0-%d", 4*i+2), Role: managed.RoleUser, Kind: "tool_result", Time: at.Add(time.Second),
				Content: strings.ReplaceAll(result, `\n`, "\n"),
				Raw:     jsontext.Value(fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":"%s"}]}}`, i, result)),
			},
			{
				ID: fmt.Sprintf("worker-0-%d", 4*i+3), Role: managed.RoleSystem, Kind: "usage", Time: at.Add(1500 * time.Millisecond),
				Usage: &managed.Usage{
					Model:      managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
					InputFresh: 1200, InputCached: 900, CacheWrite5m: 700, CacheWrite1h: 200, Output: 300,
				},
				Raw: jsontext.Value(`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"total_cost_usd":0.4211}`),
			},
		}
		for _, e := range batch {
			line, err := json.Marshal(e)
			if err != nil {
				panic(err)
			}
			size += len(line) + 1
		}
		events = append(events, batch...)
	}
	return events
}

var sessionSizes = []struct {
	name   string
	target int
}{
	{"small", 500},
	{"medium", 10 << 10},
	{"large", 200 << 10},
}

// BenchmarkJSONSessionEventsMarshal is the write side of the transcript: one
// AppendEvents call per harness batch, one JSON line per event.
func BenchmarkJSONSessionEventsMarshal(b *testing.B) {
	for _, size := range sessionSizes {
		b.Run(size.name, func(b *testing.B) {
			s := benchSession(b)
			events := benchEvents(size.target)
			if err := s.AppendEvents(events); err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(s.dir, eventsFile)
			info, err := os.Stat(path)
			if err != nil {
				b.Fatal(err)
			}
			b.Logf("payload_bytes=%d events=%d", info.Size(), len(events))
			b.ReportAllocs()
			b.SetBytes(info.Size())
			for b.Loop() {
				if err := os.Remove(path); err != nil {
					b.Fatal(err)
				}
				if err := s.AppendEvents(events); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONSessionEventsUnmarshal is the read side: Events scans the
// transcript file and decodes one event per line.
func BenchmarkJSONSessionEventsUnmarshal(b *testing.B) {
	for _, size := range sessionSizes {
		b.Run(size.name, func(b *testing.B) {
			s := benchSession(b)
			events := benchEvents(size.target)
			if err := s.AppendEvents(events); err != nil {
				b.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(s.dir, eventsFile))
			if err != nil {
				b.Fatal(err)
			}
			b.Logf("payload_bytes=%d events=%d", info.Size(), len(events))
			b.ReportAllocs()
			b.SetBytes(info.Size())
			for b.Loop() {
				got, err := s.Events()
				if err != nil {
					b.Fatal(err)
				}
				if len(got) != len(events) {
					b.Fatalf("read %d events, want %d", len(got), len(events))
				}
			}
		})
	}
}

// BenchmarkJSONSessionStatusRoundTrip writes the session's last observation
// with the indented encoder and reads it back, as a poll from a fresh process
// does.
func BenchmarkJSONSessionStatusRoundTrip(b *testing.B) {
	s := benchSession(b)
	if err := os.MkdirAll(filepath.Join(s.dir, filepath.Dir(statusFile)), 0o700); err != nil {
		b.Fatal(err)
	}
	started := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	status := managed.Status{
		Ref:       managed.SessionRef{Runtime: Name, ID: s.id, Revision: 7, LocatorVersion: 1, Locator: jsontext.Value(`{"version":1,"dir":"/var/lib/replichai/sessions/7f3a91c2","stages":[{"name":"worker-0","offset":4096}]}`)},
		Model:     managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
		StartedAt: started, FinishedAt: started.Add(41 * time.Minute),
		Usage: managed.Usage{
			Model:      managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
			InputFresh: 182000, InputCached: 910000, CacheWrite5m: 64000, CacheWrite1h: 12000, Output: 41000,
		},
	}
	if err := s.WriteStatus(status); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.dir, statusFile))
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("payload_bytes=%d", info.Size())
	b.ReportAllocs()
	b.SetBytes(info.Size())
	for b.Loop() {
		if err := s.WriteStatus(status); err != nil {
			b.Fatal(err)
		}
		if _, err := s.ReadStatus(); err != nil {
			b.Fatal(err)
		}
	}
}

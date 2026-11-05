// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// benchTranscript is a transcript.jsonl of about target bytes: an agent
// message, a tool call with its input, a tool result, and a charged model
// request, repeated. Raw carries the backend's original event, which is the
// bulk of a real transcript.
func benchTranscript(target int) ([]byte, int) {
	var sb strings.Builder
	at := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	events := 0
	for i := 0; sb.Len() < target; i++ {
		at = at.Add(1500 * time.Millisecond)
		msg := provider.Event{
			Role: provider.RoleAgent, Kind: provider.KindMessage, Time: at,
			Content: fmt.Sprintf("Step %d: fetch the evaluation script and run it against the held-out split with seed %d.", i, i),
			Raw:     json.RawMessage(fmt.Sprintf(`{"type":"agent.message","id":"evt_%04d","processed_at":%q,"content":[{"type":"text","text":"Step %d: fetch the evaluation script and run it against the held-out split with seed %d."}]}`, i, at.Format(time.RFC3339Nano), i, i)),
		}
		use := provider.Event{
			Role: provider.RoleAgent, Kind: provider.KindToolUse, Tool: "bash", Time: at.Add(100 * time.Millisecond),
			Content: fmt.Sprintf(`{"command":"python eval.py --split %d --seed %d --epochs 40"}`, i%7, i),
			Raw:     json.RawMessage(fmt.Sprintf(`{"type":"agent.tool_use","id":"toolu_%04d","name":"bash","input":{"command":"python eval.py --split %d --seed %d --epochs 40"}}`, i, i%7, i)),
		}
		out := strings.Repeat(fmt.Sprintf("epoch %d loss 0.%04d acc 0.8%03d\\n", i, i%9999, i%999), 6)
		res := provider.Event{
			Role: provider.RoleUser, Kind: provider.KindToolResult, Time: at.Add(900 * time.Millisecond),
			Content: strings.ReplaceAll(out, `\n`, "\n"),
			Raw:     json.RawMessage(fmt.Sprintf(`{"type":"user.tool_result","tool_use_id":"toolu_%04d","content":[{"type":"text","text":"%s"}]}`, i, out)),
		}
		req := provider.Event{
			Role: provider.RoleSystem, Kind: "model_request_end", Time: at.Add(time.Second),
			Usage: &provider.Usage{InputTokens: 1200 + int64(i), OutputTokens: 300, CachedInputTokens: 900, CacheWriteTokens: 400},
			Raw:   json.RawMessage(fmt.Sprintf(`{"type":"span.model_request_end","model_usage":{"input_tokens":%d,"output_tokens":300,"cache_read_input_tokens":900,"cache_creation_input_tokens":400}}`, 1200+i)),
		}
		for _, e := range []provider.Event{msg, use, res, req} {
			line, err := json.Marshal(e)
			if err != nil {
				panic(err)
			}
			sb.Write(line)
			sb.WriteByte('\n')
			events++
		}
	}
	return []byte(sb.String()), events
}

var transcriptSizes = []struct {
	name   string
	target int
}{
	{"small", 500},
	{"medium", 10 << 10},
	{"large", 200 << 10},
}

func BenchmarkJSONTranscriptParseUnmarshal(b *testing.B) {
	for _, size := range transcriptSizes {
		b.Run(size.name, func(b *testing.B) {
			raw, _ := benchTranscript(size.target)
			b.Logf("payload_bytes=%d", len(raw))
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				if _, err := parseTranscriptEvents(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONTranscriptResponseEncode is the response half of the transcript
// viewer: raw payloads dropped, usage paired, the body written as JSON.
func BenchmarkJSONTranscriptResponseEncode(b *testing.B) {
	for _, size := range transcriptSizes {
		b.Run(size.name, func(b *testing.B) {
			raw, _ := benchTranscript(size.target)
			events, err := parseTranscriptEvents(raw)
			if err != nil {
				b.Fatal(err)
			}
			shown, derived, _ := attachUsage(events)
			body := Transcript{Events: shown, Usage: &derived}
			rec := httptest.NewRecorder()
			writeJSON(rec, http.StatusOK, body)
			b.Logf("payload_bytes=%d", rec.Body.Len())
			b.ReportAllocs()
			b.SetBytes(int64(rec.Body.Len()))
			for b.Loop() {
				w := httptest.NewRecorder()
				writeJSON(w, http.StatusOK, body)
				if w.Code != http.StatusOK {
					b.Fatalf("status = %d", w.Code)
				}
			}
		})
	}
}

// benchRepro serves a fixed transcript through the forge interface.
type benchRepro struct {
	fakeRepro
	transcript []byte
}

func (r *benchRepro) Transcript(context.Context, string) ([]byte, error) { return r.transcript, nil }

// BenchmarkJSONTranscriptHandlerRoundTrip drives GET /jobs/{id}/transcript
// through the router: forge read, parse, usage pairing, response encode.
func BenchmarkJSONTranscriptHandlerRoundTrip(b *testing.B) {
	for _, size := range transcriptSizes {
		b.Run(size.name, func(b *testing.B) {
			raw, _ := benchTranscript(size.target)
			jobs := job.NewMemStore()
			d := Deps{
				Jobs:          jobs,
				Projects:      project.NewMemStore(project.WithSessions(jobs)),
				Members:       access.NewMemMembership(),
				Profiles:      profile.NewMemStore(),
				Reproductions: &benchRepro{transcript: raw},
				Gate:          GateFunc(func(*http.Request) (bool, string) { return true, "" }),
			}
			ctx := context.Background()
			p, err := d.Projects.Create(ctx, "me", "t")
			if err != nil {
				b.Fatal(err)
			}
			j, err := jobs.Enqueue(ctx, job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
			if err != nil {
				b.Fatal(err)
			}
			for _, phase := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
				if err := jobs.Advance(ctx, j.ID, phase, func(jb *job.Job) {
					if phase == job.PhasePublishing {
						jb.RepoName = "open-hai/r1"
					}
				}); err != nil {
					b.Fatal(err)
				}
			}
			h := NewRouter(d).Handler()
			serve := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/jobs/"+j.ID+"/transcript", nil)
				r = r.WithContext(WithPrincipal(r.Context(), "me"))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			first := serve()
			if first.Code != http.StatusOK {
				b.Fatalf("status = %d: %s", first.Code, first.Body.String())
			}
			b.Logf("payload_bytes=%d request_transcript_bytes=%d", first.Body.Len(), len(raw))
			b.ReportAllocs()
			b.SetBytes(int64(first.Body.Len()))
			for b.Loop() {
				if w := serve(); w.Code != http.StatusOK {
					b.Fatalf("status = %d", w.Code)
				}
			}
		})
	}
}

func benchJob(i int) job.Job {
	at := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
	return job.Job{
		ID: fmt.Sprintf("job_%08x", 0x7f3a91c2+i), Kind: job.KindAudit, Principal: "5d0c1f7e-3b1a-4c64-9a0e-2f4f6c1d8b11",
		ProjectID: "prj_4e1b9a", Ref: "arxiv:2603.01795", PaperKey: "arxiv:2603.01795", Phase: job.PhaseRunning,
		Status: provider.RunStatus{
			RunID: "sesn_01HZX3K9", Model: "claude-opus-5", Runtime: "anthropic-managed",
			Usage: provider.Usage{InputTokens: 182000, OutputTokens: 41000, CachedInputTokens: 910000, CacheWriteTokens: 64000},
		},
		RepoName: "open-hai/repro-2603-01795", CreatedAt: at, UpdatedAt: at.Add(11 * time.Minute),
		PhaseAt: map[job.Phase]time.Time{
			job.PhaseSubmitted: at, job.PhaseResolving: at.Add(time.Second), job.PhaseRunning: at.Add(20 * time.Second),
		},
		Runtime: "anthropic-managed", Model: "anthropic/claude-opus-5",
	}
}

// BenchmarkJSONJobSnapshotMarshal is one Server-Sent Events data frame: the
// snapshot of a session that the status stream sends on every change.
func BenchmarkJSONJobSnapshotMarshal(b *testing.B) {
	j := benchJob(0)
	size := len(mustJSON(j))
	b.Logf("payload_bytes=%d", size)
	b.ReportAllocs()
	b.SetBytes(int64(size))
	for b.Loop() {
		if len(mustJSON(j)) == 0 {
			b.Fatal("empty snapshot")
		}
	}
}

// BenchmarkJSONSessionListEncode is GET /jobs: the caller's sessions, newest
// first, encoded as one array.
func BenchmarkJSONSessionListEncode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 20}, {"large", 400}} {
		b.Run(size.name, func(b *testing.B) {
			jobs := make([]job.Job, size.n)
			for i := range jobs {
				jobs[i] = benchJob(i)
			}
			rec := httptest.NewRecorder()
			writeJSON(rec, http.StatusOK, jobs)
			b.Logf("payload_bytes=%d", rec.Body.Len())
			b.ReportAllocs()
			b.SetBytes(int64(rec.Body.Len()))
			for b.Loop() {
				w := httptest.NewRecorder()
				writeJSON(w, http.StatusOK, jobs)
				if w.Code != http.StatusOK {
					b.Fatalf("status = %d", w.Code)
				}
			}
		})
	}
}

// BenchmarkJSONAddPaperDecode is POST /projects/{id}/papers: a small request
// body decoded by the handler, the paper recorded, and the reply encoded.
func BenchmarkJSONAddPaperDecode(b *testing.B) {
	jobs := job.NewMemStore()
	d := Deps{
		Jobs:          jobs,
		Projects:      project.NewMemStore(project.WithSessions(jobs)),
		Members:       access.NewMemMembership(),
		Profiles:      profile.NewMemStore(),
		Reproductions: &fakeRepro{},
		Gate:          GateFunc(func(*http.Request) (bool, string) { return true, "" }),
	}
	p, err := d.Projects.Create(context.Background(), "me", "t")
	if err != nil {
		b.Fatal(err)
	}
	h := NewRouter(d).Handler()
	// Each request names a new paper, so every iteration takes the add path
	// rather than the already-recorded one.
	next := 0
	bodyFor := func() string {
		next++
		return fmt.Sprintf(`{"url":"https://arxiv.org/abs/2603.%05d"}`, next)
	}
	serve := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/projects/"+p.ID+"/papers", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(WithPrincipal(r.Context(), "me"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	sample := bodyFor()
	first := serve(sample)
	if first.Code >= 300 {
		b.Fatalf("status = %d: %s", first.Code, first.Body.String())
	}
	b.Logf("payload_bytes=%d response_bytes=%d", len(sample), first.Body.Len())
	b.ReportAllocs()
	b.SetBytes(int64(len(sample)))
	for b.Loop() {
		if w := serve(bodyFor()); w.Code >= 300 {
			b.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
	}
}

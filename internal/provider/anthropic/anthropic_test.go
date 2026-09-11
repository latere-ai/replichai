// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/latere-ai/replichai/internal/provider"
)

// newTestProvider points the adapter at a local server, so every test
// exercises the real SDK wire path without touching the network.
func newTestProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	p, err := New(WithAPIKey("test-credential"), WithAPIBase(server.URL),
		WithRequestOptions(option.WithMaxRetries(0)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}

func TestTheOptionSuppliesTheCredential(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-api-key")
		writeJSON(t, w, map[string]any{"data": []any{}, "has_more": false})
	}))
	t.Cleanup(server.Close)

	p, err := New(WithAPIKey("from-option"), WithAPIBase(server.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = p.ResolveModel(context.Background(), "best")
	if got != "from-option" {
		t.Errorf("credential = %q, want the value from WithAPIKey", got)
	}
}

func TestNoCredentialDefersToTheSDK(t *testing.T) {
	// An operator with a logged-in CLI profile should not have to restate the
	// secret. Construction therefore succeeds and a genuinely missing
	// credential fails on the first request instead.
	if _, err := New(); err != nil {
		t.Fatalf("a missing credential must not fail construction: %v", err)
	}
}

func TestTheCredentialTravelsInAHeaderNotTheURL(t *testing.T) {
	var gotURL, gotHeader string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotHeader = r.Header.Get("x-api-key")
		writeJSON(t, w, map[string]any{"data": []any{map[string]string{"id": "m"}}})
	})

	if _, err := p.ResolveModel(context.Background(), "best"); err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if strings.Contains(gotURL, "test-credential") {
		t.Errorf("credential leaked into the URL: %s", gotURL)
	}
	if gotHeader != "test-credential" {
		t.Errorf("credential not sent as a header, got %q", gotHeader)
	}
}

func TestResolveModel(t *testing.T) {
	models := func(ids ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			data := make([]map[string]string, 0, len(ids))
			for _, id := range ids {
				data = append(data, map[string]string{"id": id, "type": "model"})
			}
			writeJSON(t, w, map[string]any{"data": data, "has_more": false})
		}
	}

	t.Run("prefers the requested tier", func(t *testing.T) {
		p := newTestProvider(t, models("some-haiku-1", "some-opus-2"))
		got, err := p.ResolveModel(context.Background(), "best")
		if err != nil || got != "some-opus-2" {
			t.Fatalf("got %q, %v; want some-opus-2", got, err)
		}
	})

	t.Run("falls back to the first model", func(t *testing.T) {
		p := newTestProvider(t, models("only-model"))
		got, err := p.ResolveModel(context.Background(), "best")
		if err != nil || got != "only-model" {
			t.Fatalf("got %q, %v; want only-model", got, err)
		}
	})

	t.Run("errors when nothing is offered", func(t *testing.T) {
		p := newTestProvider(t, models())
		if _, err := p.ResolveModel(context.Background(), "best"); err == nil {
			t.Fatal("expected an error when the backend returns no models")
		}
	})
}

func TestStartRunSendsTheTaskAndRubric(t *testing.T) {
	var kickoff map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &kickoff); err != nil {
				t.Errorf("decoding kickoff: %v", err)
			}
			writeJSON(t, w, map[string]any{"events": []any{}})
			return
		}
		writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session"})
	})

	handle, err := p.StartRun(context.Background(),
		provider.WorkerRef{ID: "agent_1"},
		provider.WorkspaceRef{ID: "env_1"},
		provider.RunSpec{Task: "do it", Rubric: "check it", MaxRevisions: 5},
	)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if handle.ID != "sesn_1" {
		t.Errorf("handle.ID = %q, want sesn_1", handle.ID)
	}

	events, _ := kickoff["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("want 1 kickoff event, got %d", len(events))
	}
	event, _ := events[0].(map[string]any)
	if event["type"] != "user.define_outcome" {
		t.Errorf("event type = %v, want user.define_outcome", event["type"])
	}
	if event["description"] != "do it" {
		t.Errorf("description = %v, want %q", event["description"], "do it")
	}
	if event["max_iterations"] != float64(5) {
		t.Errorf("max_iterations = %v, want 5", event["max_iterations"])
	}
	rubric, _ := event["rubric"].(map[string]any)
	if rubric["content"] != "check it" {
		t.Errorf("rubric content = %v, want %q", rubric["content"], "check it")
	}
}

func TestStartRunMountsARepository(t *testing.T) {
	var sessionBody map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			writeJSON(t, w, map[string]any{"events": []any{}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sessionBody)
		writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session"})
	})

	_, err := p.StartRun(context.Background(),
		provider.WorkerRef{ID: "agent_1"},
		provider.WorkspaceRef{ID: "env_1"},
		provider.RunSpec{
			Task: "do it", Rubric: "check", MaxRevisions: 1,
			Mount: provider.RepoMount{
				URL:       "https://github.com/open-hai/x",
				Token:     "mount-token",
				MountPath: "/workspace/repo",
			},
		},
	)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	resources, _ := sessionBody["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("want one mounted resource, got %v", sessionBody["resources"])
	}
	res, _ := resources[0].(map[string]any)
	if res["type"] != "github_repository" {
		t.Errorf("resource type = %v, want github_repository", res["type"])
	}
	if res["url"] != "https://github.com/open-hai/x" {
		t.Errorf("url = %v", res["url"])
	}
	if res["authorization_token"] != "mount-token" {
		t.Errorf("the mount token was not passed through")
	}
	if res["mount_path"] != "/workspace/repo" {
		t.Errorf("mount_path = %v", res["mount_path"])
	}
}

func TestStartRunWithoutMountSendsNoResources(t *testing.T) {
	var sessionBody map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			writeJSON(t, w, map[string]any{"events": []any{}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sessionBody)
		writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session"})
	})

	_, err := p.StartRun(context.Background(),
		provider.WorkerRef{ID: "a"}, provider.WorkspaceRef{ID: "e"},
		provider.RunSpec{Task: "t", Rubric: "r"},
	)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, ok := sessionBody["resources"]; ok {
		t.Errorf("an audit with no mount must send no resources, got %v", sessionBody["resources"])
	}
}

func TestUngradedStartSendsTaskWithoutOutcome(t *testing.T) {
	var kickoff map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			var body struct {
				Events []map[string]any `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode kickoff: %v", err)
			}
			kickoff = body.Events[0]
			writeJSON(t, w, map[string]any{"events": []any{}})
			return
		}
		writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session"})
	})

	if _, err := p.StartRun(context.Background(), provider.WorkerRef{ID: "a"}, provider.WorkspaceRef{ID: "e"}, provider.RunSpec{Task: "run exactly once"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if kickoff["type"] != "user.message" {
		t.Fatalf("kickoff = %v, want user.message", kickoff)
	}
	if _, exists := kickoff["rubric"]; exists {
		t.Fatalf("ungraded kickoff contains rubric: %v", kickoff)
	}
	content, _ := kickoff["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("kickoff content = %v", kickoff)
	}
	block, _ := content[0].(map[string]any)
	if block["text"] != "run exactly once" {
		t.Fatalf("kickoff task = %v", kickoff)
	}
}

func TestTranscriptTranslatesEvents(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"has_more": false,
			"data": []any{
				map[string]any{
					"type":         "agent.message",
					"content":      []any{map[string]any{"type": "text", "text": "hello"}},
					"processed_at": "2026-07-25T10:00:00Z",
				},
				map[string]any{"type": "agent.thinking", "processed_at": "2026-07-25T10:00:01Z"},
				map[string]any{
					"type": "agent.tool_use", "name": "bash",
					"input":        map[string]any{"command": "ls"},
					"processed_at": "2026-07-25T10:00:02Z",
				},
				map[string]any{
					"type": "span.model_request_end", "occurred_at": "2026-07-25T10:00:03Z",
					"model_usage": map[string]any{
						"input_tokens": 100, "output_tokens": 50,
						"cache_read_input_tokens": 10, "cache_creation_input_tokens": 20,
					},
				},
			},
		})
	})

	events, err := p.Transcript(context.Background(), provider.RunHandle{ID: "sesn_1"})
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d", len(events))
	}
	if events[0].Role != "agent" || events[0].Kind != "message" || events[0].Content != "hello" {
		t.Errorf("message event = %+v", events[0])
	}
	if events[1].Kind != "thinking" || events[1].Content != "" {
		t.Errorf("thinking event = %+v", events[1])
	}
	if events[2].Kind != "tool_use" || events[2].Tool != "bash" || !strings.Contains(events[2].Content, `"command":"ls"`) {
		t.Errorf("tool_use event = %+v", events[2])
	}
	if events[3].Role != "system" || events[3].Kind != "model_request_end" {
		t.Errorf("span event = %+v", events[3])
	}
	wantUsage := provider.Usage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10, CacheWriteTokens: 20}
	if events[3].Usage == nil || *events[3].Usage != wantUsage {
		t.Errorf("normalized event usage = %+v, want %+v", events[3].Usage, wantUsage)
	}
	if len(events[0].Raw) == 0 {
		t.Error("Raw should preserve the original event")
	}
}

func TestPollTranslatesStatesAndGrades(t *testing.T) {
	cases := []struct {
		name          string
		backendStatus string
		backendGrade  string
		wantState     provider.RunState
		wantGrade     provider.Grade
	}{
		{"running", "running", "", provider.StateRunning, provider.GradeUnknown},
		{"passed", "idle", "satisfied", provider.StateDone, provider.GradePassed},
		{"needs revision", "idle", "needs_revision", provider.StateDone, provider.GradeNeedsRevision},
		{"exhausted", "idle", "max_iterations_reached", provider.StateDone, provider.GradeExhausted},
		{"failed", "terminated", "failed", provider.StateDone, provider.GradeFailed},
		{"grader still working", "running", "pending", provider.StateRunning, provider.GradePending},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{"id": "sesn_1", "type": "session", "status": tc.backendStatus}
				if tc.backendGrade != "" {
					body["outcome_evaluations"] = []map[string]any{
						{"result": tc.backendGrade, "explanation": "because"},
					}
				}
				writeJSON(t, w, body)
			})

			status, err := p.Poll(context.Background(), provider.RunHandle{ID: "sesn_1"})
			if err != nil {
				t.Fatalf("Poll: %v", err)
			}
			if status.State != tc.wantState {
				t.Errorf("state = %s, want %s", status.State, tc.wantState)
			}
			gotGrade := provider.GradeUnknown
			if status.Verdict != nil {
				gotGrade = status.Verdict.Grade
			}
			if gotGrade != tc.wantGrade {
				t.Errorf("grade = %s, want %s", gotGrade, tc.wantGrade)
			}
		})
	}
}

func TestARunThatHasNotStartedIsNotReportedAsFinished(t *testing.T) {
	// Regression: the backend reports the same resting state both before
	// work begins and after it ends. This was observed against the live
	// backend; treating it as terminal aborts healthy runs whose first poll
	// lands between kickoff and pickup.
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session", "status": "idle"})
	})

	status, err := p.Poll(context.Background(), provider.RunHandle{ID: "sesn_1"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if status.State.Terminal() {
		t.Errorf("state %s is terminal; a not-yet-started run must not be", status.State)
	}
	if status.State != provider.StatePending {
		t.Errorf("state = %s, want pending", status.State)
	}
}

func TestSilenceFromTheGraderNeverReadsAsSuccess(t *testing.T) {
	for _, backendStatus := range []string{"idle", "terminated", "running"} {
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"id": "s", "type": "session", "status": backendStatus})
		})
		status, err := p.Poll(context.Background(), provider.RunHandle{ID: "s"})
		if err != nil {
			t.Fatalf("Poll(%s): %v", backendStatus, err)
		}
		if status.Passed() {
			t.Errorf("status %s with no grader record reported as passed", backendStatus)
		}
	}
}

func TestListAndFetchArtifacts(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Write([]byte("file body"))
			return
		}
		if got := r.URL.Query().Get("scope_id"); got != "sesn_1" {
			t.Errorf("scope_id = %q, want sesn_1", got)
		}
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{{
				"id": "file_1", "filename": "src/core.py", "type": "file",
			}},
			"has_more": false,
		})
	})

	handle := provider.RunHandle{ID: "sesn_1"}
	arts, err := p.ListArtifacts(context.Background(), handle)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 1 || arts[0].Path != "src/core.py" || arts[0].ID != "file_1" {
		t.Fatalf("got %+v, want one artifact src/core.py", arts)
	}

	body, err := p.FetchArtifact(context.Background(), handle, arts[0])
	if err != nil {
		t.Fatalf("FetchArtifact: %v", err)
	}
	if string(body) != "file body" {
		t.Errorf("body = %q, want %q", body, "file body")
	}
}

func TestCloseRunToleratesAnAlreadyClosedRun(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"already archived"}}`, http.StatusConflict)
	})
	if err := p.CloseRun(context.Background(), provider.RunHandle{ID: "sesn_1"}); err != nil {
		t.Errorf("CloseRun should be best-effort, got %v", err)
	}
}

// A session already archived by an earlier close, such as a cancel, rejects the
// interrupt with 400 rather than 404 or 409. That is the requested end state,
// so it must not be reported as a failed close and turn a finished run into a
// failed one.
func TestCloseRunToleratesAnAlreadyArchivedSession(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions/sesn_1/events":
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Cannot send events to archived session: sesn_1"}}`, http.StatusBadRequest)
		default:
			writeJSON(t, w, map[string]any{"id": "sesn_1"})
		}
	})
	if err := p.CloseRun(context.Background(), provider.RunHandle{ID: "sesn_1"}); err != nil {
		t.Errorf("CloseRun on an archived session = %v, want nil", err)
	}
}

// Tolerating the archived case must not widen into tolerating every 400: a
// malformed interrupt payload is also a 400, and swallowing it would leave live
// sessions running.
func TestCloseRunReportsOtherBadRequests(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions/sesn_1/events":
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"events.0.type: unknown event type"}}`, http.StatusBadRequest)
		default:
			writeJSON(t, w, map[string]any{"id": "sesn_1"})
		}
	})
	err := p.CloseRun(context.Background(), provider.RunHandle{ID: "sesn_1"})
	if err == nil || !strings.Contains(err.Error(), "interrupt") {
		t.Fatalf("CloseRun error = %v, want the interrupt failure reported", err)
	}
}

// Closing a live managed-agent session must interrupt its work before archiving
// it. Archival alone hides the session but leaves the agent running.
func TestCloseRunInterruptsBeforeArchiving(t *testing.T) {
	var paths []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/sessions/sesn_1/events":
			var body struct {
				Events []struct {
					Type string `json:"type"`
				} `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode interrupt: %v", err)
			}
			if len(body.Events) != 1 || body.Events[0].Type != "user.interrupt" {
				t.Fatalf("close event = %+v, want one user.interrupt", body.Events)
			}
			writeJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/sessions/sesn_1/archive":
			writeJSON(t, w, map[string]any{"id": "sesn_1"})
		default:
			t.Fatalf("unexpected close request: %s", r.URL.Path)
		}
	})

	if err := p.CloseRun(context.Background(), provider.RunHandle{ID: "sesn_1"}); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	want := []string{"/v1/sessions/sesn_1/events", "/v1/sessions/sesn_1/archive"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("close requests = %v, want %v", paths, want)
	}
}

func TestCloseRunReturnsFailuresAfterTryingBothOperations(t *testing.T) {
	var paths []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		http.Error(w, `{"error":{"message":"backend down"}}`, http.StatusInternalServerError)
	})

	err := p.CloseRun(context.Background(), provider.RunHandle{ID: "sesn_1"})
	if err == nil || !strings.Contains(err.Error(), "interrupt") || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("CloseRun error = %v, want joined interrupt and archive failures", err)
	}
	want := []string{"/v1/sessions/sesn_1/events", "/v1/sessions/sesn_1/archive"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("close requests = %v, want %v", paths, want)
	}
}

func TestEnsureWorkspaceAndWorkerCarryTheSpec(t *testing.T) {
	var payloads []map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		payloads = append(payloads, decoded)
		writeJSON(t, w, map[string]any{"id": "id_1", "version": 3})
	})

	ws, err := p.EnsureWorkspace(context.Background(), provider.WorkspaceSpec{
		Name:     "workspace",
		Packages: map[string][]string{"pip": {"pypdf"}},
	})
	if err != nil || ws.ID != "id_1" {
		t.Fatalf("EnsureWorkspace: %v (%+v)", err, ws)
	}
	config, _ := payloads[0]["config"].(map[string]any)
	networking, _ := config["networking"].(map[string]any)
	if networking["type"] != "unrestricted" {
		t.Errorf("networking = %v, want unrestricted", networking["type"])
	}

	worker, err := p.EnsureWorker(context.Background(), provider.WorkerSpec{
		Name: "worker", Instructions: "audit papers", Model: "some-model",
	})
	if err != nil {
		t.Fatalf("EnsureWorker: %v", err)
	}
	if worker.Revision != 3 {
		t.Errorf("revision = %d, want 3", worker.Revision)
	}
	if payloads[1]["system"] != "audit papers" {
		t.Errorf("system = %v, want %q", payloads[1]["system"], "audit papers")
	}
	model, _ := payloads[1]["model"].(map[string]any)
	if model["id"] != "some-model" {
		t.Errorf("model id = %v, want some-model", model["id"])
	}
}

func TestOutputDirIsBackendSpecific(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	if p.OutputDir() == "" || !strings.HasPrefix(p.OutputDir(), "/") {
		t.Errorf("OutputDir must be an absolute path, got %q", p.OutputDir())
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name = %q, want anthropic", p.Name())
	}
}

func TestBackendErrorsSurfaceFromEveryCall(t *testing.T) {
	// A 500 from any endpoint must propagate, not be swallowed. CloseRun is
	// deliberately excluded: releasing a run is best-effort.
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	})
	ctx := context.Background()
	handle := provider.RunHandle{ID: "sesn_1"}

	calls := map[string]func() error{
		"ResolveModel": func() error {
			_, err := p.ResolveModel(ctx, "best")
			return err
		},
		"EnsureWorkspace": func() error {
			_, err := p.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "w"})
			return err
		},
		"EnsureWorker": func() error {
			_, err := p.EnsureWorker(ctx, provider.WorkerSpec{Name: "n", Model: "m"})
			return err
		},
		"StartRun": func() error {
			_, err := p.StartRun(ctx, provider.WorkerRef{}, provider.WorkspaceRef{}, provider.RunSpec{})
			return err
		},
		"Poll": func() error {
			_, err := p.Poll(ctx, handle)
			return err
		},
		"ListArtifacts": func() error {
			_, err := p.ListArtifacts(ctx, handle)
			return err
		},
		"FetchArtifact": func() error {
			_, err := p.FetchArtifact(ctx, handle, provider.Artifact{ID: "f", Path: "x"})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s swallowed a backend error", name)
		}
	}
}

func TestStartRunFailsWhenTheKickoffIsRejected(t *testing.T) {
	// The session is created before the kickoff is sent; a rejected kickoff
	// must fail the call rather than return a handle to a run that will
	// never start.
	deleted := false
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
			writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			http.Error(w, `{"error":{"message":"bad rubric"}}`, http.StatusBadRequest)
			return
		}
		writeJSON(t, w, map[string]any{"id": "sesn_1", "type": "session"})
	})

	if _, err := p.StartRun(context.Background(),
		provider.WorkerRef{ID: "a"}, provider.WorkspaceRef{ID: "e"},
		provider.RunSpec{Task: "t", Rubric: "r"},
	); err == nil {
		t.Fatal("a rejected kickoff must fail StartRun")
	}
	if !deleted {
		t.Fatal("a rejected kickoff orphaned the created session")
	}
}

func TestStartRunRollsBackEarlierUploadWhenSecondFails(t *testing.T) {
	uploads, deleted := 0, []string{}
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			writeJSON(t, w, map[string]any{"id": "file_1", "type": "file"})
			return
		}
		if strings.Contains(r.URL.Path, "/files") {
			uploads++
			if uploads == 2 {
				http.Error(w, `{"error":{"message":"upload failed"}}`, http.StatusInternalServerError)
				return
			}
			writeJSON(t, w, map[string]any{"id": "file_1", "type": "file", "filename": "one", "mime_type": "text/plain", "size_bytes": 1, "created_at": "2026-01-01T00:00:00Z"})
			return
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	_, err := p.StartRun(context.Background(), provider.WorkerRef{ID: "a"}, provider.WorkspaceRef{ID: "e"}, provider.RunSpec{
		Task: "task", Attachments: []provider.Attachment{{Path: "/one", Content: []byte("1")}, {Path: "/two", Content: []byte("2")}},
	})
	if err == nil {
		t.Fatal("second upload failure was ignored")
	}
	if len(deleted) != 1 || !strings.Contains(deleted[0], "file_1") {
		t.Fatalf("rollback deletes = %v, want file_1", deleted)
	}
}

func TestCloseRunDeletesPersistedFilesAndReleasesResources(t *testing.T) {
	var requests []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		writeJSON(t, w, map[string]any{})
	})
	handle := provider.RunHandle{ID: "sesn_1", Files: []string{"file_1"}}
	if err := p.CloseRun(context.Background(), handle); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	if err := p.ReleaseResources(context.Background(), provider.WorkerRef{ID: "agent_1"}, provider.WorkspaceRef{ID: "env_1"}); err != nil {
		t.Fatalf("ReleaseResources: %v", err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"DELETE /v1/files/file_1", "POST /v1/agents/agent_1/archive", "DELETE /v1/environments/env_1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("requests missing %q:\n%s", want, joined)
		}
	}
}

func TestLimitedNetworkIsRequestedWhenAsked(t *testing.T) {
	var payload map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		writeJSON(t, w, map[string]any{"id": "env_1"})
	})

	if _, err := p.EnsureWorkspace(context.Background(), provider.WorkspaceSpec{
		Name: "w", Network: provider.NetworkLimited,
	}); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	config, _ := payload["config"].(map[string]any)
	networking, _ := config["networking"].(map[string]any)
	if networking["type"] != "limited" {
		t.Errorf("networking = %v, want limited", networking["type"])
	}
}

func TestWithRequestOptionsIsApplied(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-marker")
		writeJSON(t, w, map[string]any{"data": []any{}, "has_more": false})
	}))
	t.Cleanup(server.Close)

	p, err := New(
		WithAPIKey("k"),
		WithAPIBase(server.URL),
		WithRequestOptions(option.WithHeader("x-marker", "applied")),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = p.ResolveModel(context.Background(), "best")
	if gotHeader != "applied" {
		t.Errorf("request option not applied, header = %q", gotHeader)
	}
}

func TestATerminatedRunIsTerminal(t *testing.T) {
	// Regression: "terminated" is irreversible, so a run that reaches it
	// without a grader record will never produce one. Reporting it as
	// pending made the auditor poll a dead run until its deadline expired.
	// State is the lifecycle and grade is the outcome. A run the grader
	// failed still completed, so it is Done and not passed; only a run that
	// died without being graded is Failed. The translation exists to keep
	// the two apart.
	cases := []struct {
		name      string
		grade     string
		wantState provider.RunState
		wantPass  bool
	}{
		{"died before grading", "", provider.StateFailed, false},
		{"graded, then released", "satisfied", provider.StateDone, true},
		{"completed but graded as failed", "failed", provider.StateDone, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{"id": "s", "type": "session", "status": "terminated"}
				if tc.grade != "" {
					body["outcome_evaluations"] = []map[string]any{{"result": tc.grade}}
				}
				writeJSON(t, w, body)
			})

			status, err := p.Poll(context.Background(), provider.RunHandle{ID: "s"})
			if err != nil {
				t.Fatalf("Poll: %v", err)
			}
			if !status.State.Terminal() {
				t.Errorf("state %s is not terminal; a terminated run always is", status.State)
			}
			if status.State != tc.wantState {
				t.Errorf("state = %s, want %s", status.State, tc.wantState)
			}
			if status.Passed() != tc.wantPass {
				t.Errorf("Passed() = %v, want %v", status.Passed(), tc.wantPass)
			}
		})
	}
}

func TestTranslateStateReadsStatusAndVerdictTogether(t *testing.T) {
	// Neither field answers alone: the backend rests in "idle" both before a
	// run starts and after it ends, and reaches "terminated" both by
	// finishing and by dying.
	passed := &provider.Verdict{Grade: provider.GradePassed}
	pending := &provider.Verdict{Grade: provider.GradePending}

	cases := []struct {
		status  string
		verdict *provider.Verdict
		want    provider.RunState
	}{
		{"idle", nil, provider.StatePending},
		{"idle", pending, provider.StateRunning},
		{"idle", passed, provider.StateDone},
		{"terminated", nil, provider.StateFailed},
		{"terminated", passed, provider.StateDone},
		{"terminated", pending, provider.StateFailed},
		{"running", nil, provider.StateRunning},
		{"rescheduling", nil, provider.StateRunning},
		{"something-new", nil, provider.StateRunning},
	}
	for _, tc := range cases {
		if got := translateState(tc.status, tc.verdict); got != tc.want {
			t.Errorf("translateState(%q, %v) = %s, want %s",
				tc.status, tc.verdict, got, tc.want)
		}
	}
}

func TestTranslateGradeNeverInventsAPass(t *testing.T) {
	if got := translateGrade("a-grade-we-do-not-know"); got == provider.GradePassed {
		t.Error("an unrecognized grade must never read as a pass")
	}
	if got := translateGrade("a-grade-we-do-not-know"); got.Decided() {
		t.Errorf("an unrecognized grade must be undecided, got %s", got)
	}
}

func TestModelTierPassesUnknownPreferencesThrough(t *testing.T) {
	if got := modelTier("best"); got != "opus" {
		t.Errorf("modelTier(best) = %q, want opus", got)
	}
	if got := modelTier("some-family"); got != "some-family" {
		t.Errorf("an unknown preference should pass through, got %q", got)
	}
}

func TestPollCarriesCacheWritesSplitByLifetime(t *testing.T) {
	// A cache write is billed above fresh input, and a 1-hour write above a
	// 5-minute one, so a charge computed from a Usage that dropped them
	// under-bills a session by its most expensive input class (011). The
	// backend reports them broken down; the adapter must not flatten them.
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"id": "sesn_1", "type": "session", "status": "running",
			"usage": map[string]any{
				"input_tokens":            100,
				"output_tokens":           20,
				"cache_read_input_tokens": 5000,
				"cache_creation": map[string]any{
					"ephemeral_5m_input_tokens": 700,
					"ephemeral_1h_input_tokens": 300,
				},
			},
		})
	})

	status, err := p.Poll(context.Background(), provider.RunHandle{ID: "sesn_1"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	want := provider.Usage{
		InputTokens: 100, OutputTokens: 20, CachedInputTokens: 5000,
		CacheWrite5mTokens: 700, CacheWrite1hTokens: 300,
	}
	if status.Usage != want {
		t.Errorf("usage = %+v, want %+v", status.Usage, want)
	}
	if got, want := status.Usage.Total(), int64(6120); got != want {
		t.Errorf("total = %d, want %d — a class missing from Total is a class nobody is charged for", got, want)
	}
	if got, want := status.Usage.CacheWritten(), int64(1000); got != want {
		t.Errorf("cache written = %d, want %d", got, want)
	}
}

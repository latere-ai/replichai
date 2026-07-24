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

	p, err := New(WithAPIKey("test-credential"), WithAPIBase(server.URL))
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

func TestTheNeutralVariableSuppliesTheCredential(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-api-key")
		writeJSON(t, w, map[string]any{"data": []any{}, "has_more": false})
	}))
	t.Cleanup(server.Close)

	t.Setenv("REPLICHAI_API_KEY", "from-neutral-var")
	p, err := New(WithAPIBase(server.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = p.ResolveModel(context.Background(), "best")
	if got != "from-neutral-var" {
		t.Errorf("credential = %q, want the value from REPLICHAI_API_KEY", got)
	}
}

func TestAnUnsetVariableDefersToTheSDK(t *testing.T) {
	// An operator who already has the backend's own credential exported, or a
	// logged-in CLI profile, should not have to copy the secret under a second
	// name. Construction therefore succeeds and a genuinely missing credential
	// fails on the first request instead.
	t.Setenv("REPLICHAI_API_KEY", "")
	if _, err := New(); err != nil {
		t.Fatalf("an unset variable must not fail construction: %v", err)
	}
}

func TestNewReadsTheCredentialFromTheEnvironment(t *testing.T) {
	t.Setenv("REPLICHAI_API_KEY", "from-env")
	if _, err := New(); err != nil {
		t.Fatalf("New: %v", err)
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
	// Regression: the backend parks a run in the same resting state both
	// before work begins and after it ends. Observed against the live
	// backend — treating that as terminal aborts healthy runs whose first
	// poll lands between kickoff and pickup.
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
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
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

func TestRegisteredUnderItsName(t *testing.T) {
	// The init registration must make the adapter reachable by name.
	t.Setenv("REPLICHAI_API_KEY", "test-credential")
	got, err := provider.Get("anthropic")
	if err != nil {
		t.Fatalf("Get(anthropic): %v", err)
	}
	if got.Name() != "anthropic" {
		t.Errorf("Name = %q, want anthropic", got.Name())
	}
	if got.Name() != provider.DefaultName {
		t.Errorf("the adapter registers as %q but the default is %q", got.Name(), provider.DefaultName)
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
	// State is lifecycle, grade is outcome. A run the grader failed still
	// *completed*, so it is Done and not passed; only a run that died without
	// ever being graded is Failed. Collapsing the two is the conflation this
	// translation exists to avoid.
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

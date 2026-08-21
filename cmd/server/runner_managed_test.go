package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/legacy"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/provider"
	providermemory "github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/runtimes"
)

func TestEngineManagedExecutePersistsCompleteReferenceBeforeStatus(t *testing.T) {
	runtime := managedmemory.New()
	controller := managedController(t, "memory", runtime)
	store := job.NewMemStore()
	j, _ := store.Enqueue(context.Background(), job.Submission{Principal: "p", Project: "project", Ref: "paper", Kind: job.KindAudit})
	_, _, _ = store.Claim(context.Background(), "worker")
	_ = store.Advance(context.Background(), j.ID, job.PhaseRunning, nil)
	j, _ = store.Get(context.Background(), j.ID)
	engine := &engineRunner{controller: controller, method: runnerMethod, jobs: store, workDir: t.TempDir()}
	var observations int
	if err := engine.Execute(context.Background(), &j, func(status provider.RunStatus) { observations++ }); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stored, _ := store.Get(context.Background(), j.ID)
	ref, ok, err := stored.ManagedSessionRef()
	if err != nil || !ok || ref.Runtime != "memory" || len(ref.Locator) == 0 {
		t.Fatalf("persisted ref = %+v, %v, %v", ref, ok, err)
	}
	if observations < 2 || stored.Status.State != provider.StateDone {
		t.Fatalf("observations=%d status=%+v", observations, stored.Status)
	}
}

func TestEngineResumeRoutesByPersistedRuntimeAfterDefaultFlip(t *testing.T) {
	aBackend := providermemory.New()
	bBackend := providermemory.New()
	a := legacyRuntime(t, "runtime-a", aBackend)
	b := legacyRuntime(t, "runtime-b", bBackend)
	router, err := runtimes.NewRouter("runtime-a", a, b)
	if err != nil {
		t.Fatal(err)
	}
	first := managed.NewController(router, managed.WithWaitPolicy(testWaitPolicy()))
	auditor := audit.NewSessionAuditor(first, runnerMethod)
	ref, err := auditor.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	flipped, _ := router.WithDefault("runtime-b")
	controller := managed.NewController(flipped, managed.WithWaitPolicy(testWaitPolicy()))
	store := job.NewMemStore()
	j, _ := store.Enqueue(context.Background(), job.Submission{Principal: "p", Project: "project", Ref: "paper", Kind: job.KindAudit})
	_, _, _ = store.Claim(context.Background(), "worker")
	_ = store.Advance(context.Background(), j.ID, job.PhaseRunning, nil)
	raw, _ := json.Marshal(ref)
	_ = store.Observe(context.Background(), j.ID, provider.RunStatus{RunID: ref.ID, Runtime: string(ref.Runtime), SessionRef: raw})
	j, _ = store.Get(context.Background(), j.ID)
	engine := &engineRunner{controller: controller, method: runnerMethod, jobs: store, workDir: t.TempDir()}
	if err := engine.Resume(context.Background(), &j, func(provider.RunStatus) {}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(aBackend.Closed()) != 1 || len(bBackend.Runs()) != 0 {
		t.Fatalf("runtime calls A closed=%v, B runs=%v", aBackend.Closed(), bBackend.Runs())
	}
}

func TestManagedStatusConversionPreservesReferenceModelAndUsage(t *testing.T) {
	ref := managed.SessionRef{Runtime: "runtime-a", ID: "session", Revision: 2, LocatorVersion: 1}
	status := providerStatus(managed.Status{
		Ref: ref, Model: managed.ModelRef{Provider: "anthropic", ID: "model"}, State: managed.StateDone,
		Usage:   managed.Usage{InputFresh: 1, InputCached: 2, CacheWrite: 3, CacheWrite5m: 4, CacheWrite1h: 5, Output: 6},
		Verdict: &managed.Verdict{Grade: managed.GradePassed, Explanation: "ok"},
	})
	if status.RunID != ref.ID || status.Runtime != string(ref.Runtime) || status.Model != "model" || status.Usage.Total() != 21 || !status.Passed() {
		t.Fatalf("providerStatus = %+v", status)
	}
	if len(providerEvents([]managed.Event{{ID: "id", Role: managed.RoleAgent, Kind: managed.KindMessage, Content: "x"}})) != 1 {
		t.Fatal("event conversion failed")
	}
	for _, state := range []managed.State{managed.StateUnknown, managed.StatePending, managed.StateRunning, managed.StateDone, managed.StateFailed} {
		_ = providerState(state)
	}
}

func TestLiveTranscriptRoutesByPersistedReference(t *testing.T) {
	aBackend := providermemory.New()
	aBackend.Events = []provider.Event{{Role: provider.RoleAgent, Kind: provider.KindMessage, Content: "from-a"}}
	bBackend := providermemory.New()
	a := legacyRuntime(t, "runtime-a", aBackend)
	b := legacyRuntime(t, "runtime-b", bBackend)
	router, _ := runtimes.NewRouter("runtime-a", a, b)
	ref, err := managed.NewController(router, managed.WithWaitPolicy(testWaitPolicy())).Start(context.Background(), managed.SessionSpec{
		Model: managed.ModelSelector{Preference: "best"}, Instructions: "instructions", Task: "task",
		Sandbox: managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkOpen}},
	}, managed.Hooks{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	flipped, _ := router.WithDefault("runtime-b")
	raw, _ := json.Marshal(ref)
	events, err := (liveTranscript{router: flipped}).Events(context.Background(), provider.RunStatus{
		RunID: ref.ID, Runtime: string(ref.Runtime), SessionRef: raw,
	})
	if err != nil || len(events) != 1 || events[0].Content != "from-a" || len(bBackend.Runs()) != 0 {
		t.Fatalf("Events = %+v, %v; B runs=%v", events, err, bBackend.Runs())
	}
}

func managedController(t *testing.T, defaultName managed.RuntimeName, installed ...managed.Runtime) *managed.Controller {
	t.Helper()
	router, err := runtimes.NewRouter(defaultName, installed...)
	if err != nil {
		t.Fatal(err)
	}
	return managed.NewController(router, managed.WithWaitPolicy(testWaitPolicy()))
}

func testWaitPolicy() managed.WaitPolicy {
	return managed.WaitPolicy{MaxTemporaryErrors: 2, MaxSilence: time.Millisecond, CleanupTimeout: time.Second}
}

func legacyRuntime(t *testing.T, name managed.RuntimeName, backend *providermemory.Provider) managed.Runtime {
	t.Helper()
	runtime, err := legacy.New(legacy.Config{Name: name, ModelProvider: "anthropic", Provider: backend})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

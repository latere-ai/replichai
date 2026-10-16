// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"errors"
	"strings"
	"testing"

	managedmemory "latere.ai/x/managed-agents/memory"

	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
)

func newModelCohort(p provider.Provider, config PilotConfig, cov Coverage) *ModelCohort {
	return &ModelCohort{
		N: config.N, Trials: 4, Controller: managedController(p), Config: config, Coverage: cov,
	}
}

func TestModelCohortReturnsCleanupError(t *testing.T) {
	closeErr := errors.New("close failed")
	p := &closeFailProvider{Provider: memory.New(), err: closeErr}
	p.Files = map[string][]byte{CohortDataFile: []byte("participant,answer\np1,yes\n")}

	if _, err := newModelCohort(p, cohortConfig(), Coverage{Servable: []string{"task"}}).Collect(context.Background(), caseTwoInstrument().Decl); !errors.Is(err, closeErr) {
		t.Fatalf("Collect error = %v, want cleanup error", err)
	}
}

func cohortConfig() PilotConfig {
	return PilotConfig{
		N: 2, Caps: Caps{MaxParticipants: 10, MaxTrials: 50, MaxCalls: 500},
		Model: "claude-x", Snapshot: "2026-01-01",
		PromptTemplate: "Respond to each prospective-memory trial.", OrderingSeeds: []int64{1, 2},
	}
}

func TestModelCohortCollects(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{CohortDataFile: []byte("participant,condition,pm_accuracy\np1,control,0.8\np2,treatment,0.7\n")}
	p.Events = nil

	cov := Coverage{Servable: []string{"prospective-memory task"}}
	m := newModelCohort(p, cohortConfig(), cov)

	data, err := m.Collect(context.Background(), caseTwoInstrument().Decl)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(data.Header) != 3 || data.Header[0] != "participant" {
		t.Errorf("header = %v, want the cohort's columns", data.Header)
	}
	// The worker instruction names only the servable component and forbids personas.
	instr := p.Workers()[0].Instructions
	if !strings.Contains(instr, "prospective-memory task") {
		t.Error("instructions should name the servable component")
	}
	if err := AssertNoPersona(instr); err != nil {
		t.Errorf("the cohort instructions themselves carry a persona term: %v", err)
	}
}

func TestModelCohortUsesOneUngradedManagedSession(t *testing.T) {
	runtime := managedmemory.NewWithStore(nil, managedmemory.Config{Files: map[string][]byte{
		CohortDataFile: []byte("participant,answer\np1,yes\n"),
	}})
	m := &ModelCohort{
		N: 2, Trials: 4, Controller: controllerForRuntime(runtime), Config: cohortConfig(),
		Coverage: Coverage{Servable: []string{"task"}},
	}
	if _, err := m.Collect(context.Background(), caseTwoInstrument().Decl); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	specs := runtime.Specs()
	if len(specs) != 1 || specs[0].Evaluation != nil || m.Usage().Model.Provider != "memory" {
		t.Fatalf("managed specs=%+v usage=%+v", specs, m.Usage())
	}
}

// AC11: no demographic or persona attribute reaches the prompt.
func TestModelCohortRefusesPersonaPrompt(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{CohortDataFile: []byte("x\n1\n")}
	config := cohortConfig()
	config.PromptTemplate = "You are a 34-year-old participant; consider your gender when answering."
	m := newModelCohort(p, config, Coverage{Servable: []string{"task"}})

	if _, err := m.Collect(context.Background(), caseTwoInstrument().Decl); err == nil {
		t.Fatal("a persona-bearing prompt must be refused")
	}
	if len(p.Runs()) != 0 {
		t.Error("a refused prompt must never reach a run")
	}
}

// AC13: an over-cap cohort is refused before any session starts.
func TestModelCohortRespectsCaps(t *testing.T) {
	p := memory.New()
	config := cohortConfig()
	config.N = 100 // over the participant cap of 10
	m := newModelCohort(p, config, Coverage{Servable: []string{"task"}})

	if _, err := m.Collect(context.Background(), caseTwoInstrument().Decl); err == nil {
		t.Fatal("an over-cap cohort must be refused")
	}
	if len(p.Runs()) != 0 {
		t.Error("an over-cap cohort must never start a run")
	}
}

func TestModelCohortMissingDataIsAnError(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{"stray.txt": []byte("no cohort data")}
	m := newModelCohort(p, cohortConfig(), Coverage{Servable: []string{"task"}})
	if _, err := m.Collect(context.Background(), caseTwoInstrument().Decl); err == nil {
		t.Fatal("a run that produced no cohort data should error")
	}
}

func TestModelCohortRefusesNullAnalysis(t *testing.T) {
	p := memory.New()
	m := newModelCohort(p, cohortConfig(), Coverage{Servable: []string{"task"}})
	decl := caseTwoInstrument().Decl
	decl.Analysis = nil
	if _, err := m.Collect(context.Background(), decl); err == nil {
		t.Fatal("a null-analysis instrument cannot host a cohort")
	}
}

func TestModelCohortSurfacesProviderFailure(t *testing.T) {
	p := memory.New()
	p.Err = errBackendDown
	m := newModelCohort(p, cohortConfig(), Coverage{Servable: []string{"task"}})
	if _, err := m.Collect(context.Background(), caseTwoInstrument().Decl); err == nil {
		t.Fatal("a backend failure should surface")
	}
}

func TestModelCohortReportsUsage(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 2 // usage accumulates across polls in the memory provider
	p.Files = map[string][]byte{CohortDataFile: []byte("x\n1\n")}
	m := newModelCohort(p, cohortConfig(), Coverage{Servable: []string{"task"}})
	if _, err := m.Collect(context.Background(), caseTwoInstrument().Decl); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if m.Usage().TotalTokens() == 0 {
		t.Error("the cohort should report the run's usage for the pilot")
	}
}

func TestAssertNoPersonaWordBoundaries(t *testing.T) {
	// Innocent words that merely contain a persona substring must pass.
	for _, ok := range []string{"the message and its usage", "trace the average", "a package of trials"} {
		if err := AssertNoPersona(ok); err != nil {
			t.Errorf("%q should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"your age is 40", "consider gender", "assume an occupation"} {
		if err := AssertNoPersona(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

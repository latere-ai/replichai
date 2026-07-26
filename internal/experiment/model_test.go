package experiment

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/provider/memory"
)

func newModelCohort(p *memory.Provider, config PilotConfig, cov Coverage) *ModelCohort {
	return &ModelCohort{
		N: config.N, Trials: 4, Provider: p, Config: config, Coverage: cov,
		sleep: func(context.Context, time.Duration) error { return nil },
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
	if m.Usage().Total() == 0 {
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

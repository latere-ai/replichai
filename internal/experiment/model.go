package experiment

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/provider"
)

// CohortDataFile is the conforming trial data a model cohort writes and the
// cohort reads back.
const CohortDataFile = "cohort.csv"

// ErrPersonaInPrompt reports a prompt template that assigns the cohort a
// demographic or persona attribute. Inventing a population and reporting how its
// strata differ is fabricated participant data with extra steps, and it is
// refused outright (007c), not scoped for later.
var ErrPersonaInPrompt = errors.New("a model cohort prompt must carry no demographic or persona attribute")

// personaTerms are attributes a cohort prompt must never carry. Matched on word
// boundaries, so "message" and "usage" do not trip "age".
var personaTerms = regexp.MustCompile(`(?i)\b(age|aged|gender|ethnicity|race|occupation|culture|cultural|disability|persona|personas|demographic|demographics|nationality)\b`)

// AssertNoPersona reports whether a prompt is free of persona attributes. It is
// exported so the same check guards a prompt wherever one is set.
func AssertNoPersona(prompt string) error {
	if m := personaTerms.FindString(prompt); m != "" {
		return fmt.Errorf("%w: found %q", ErrPersonaInPrompt, m)
	}
	return nil
}

// ModelCohort is a model cohort: N sessions driven through the protocol's
// servable components, answering as instructed, over the existing provider seam
// — no new backend. Its marginal claim over the generator is only how a
// plausible responder behaves, so it is gated on ground truth (see
// GateModelCohort) and its data is a pilot, never evidence about people.
type ModelCohort struct {
	N        int
	Trials   int
	Provider provider.Provider
	Config   PilotConfig
	Coverage Coverage // the servable components to drive; the rest are skipped

	pollInterval time.Duration
	timeout      time.Duration
	sleep        func(context.Context, time.Duration) error
	usage        provider.Usage
}

// Kind identifies the cohort.
func (*ModelCohort) Kind() string { return CohortModel }

// Usage reports what the last Collect consumed, for the pilot.
func (m *ModelCohort) Usage() provider.Usage { return m.usage }

// Collect drives the cohort through the provider and reads back conforming trial
// data. It refuses a persona-bearing prompt and an over-cap run before any
// session starts, so neither reaches the backend. In CI the in-process provider
// returns canned data, so no model is called.
func (m *ModelCohort) Collect(ctx context.Context, decl instrument.Instrument) (data CohortData, err error) {
	if decl.Analysis == nil {
		return CohortData{}, ErrNoAnalysis
	}
	if err := AssertNoPersona(m.Config.PromptTemplate); err != nil {
		return CohortData{}, err
	}
	if err := m.Config.Caps.Admit(m.N, m.Trials); err != nil {
		return CohortData{}, err
	}

	model, err := m.Provider.ResolveModel(ctx, "best")
	if err != nil {
		return CohortData{}, fmt.Errorf("resolve model: %w", err)
	}
	ws, err := m.Provider.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "cohort-workspace", Network: provider.NetworkOpen})
	if err != nil {
		return CohortData{}, fmt.Errorf("ensure workspace: %w", err)
	}
	worker, err := m.Provider.EnsureWorker(ctx, provider.WorkerSpec{
		Name:         "repro-cohort",
		Instructions: m.instructions(decl),
		Model:        model,
		Description:  "Answers a study protocol's servable trials as a plausible participant, with no invented demographics.",
	})
	if err != nil {
		return CohortData{}, fmt.Errorf("ensure worker: %w", err)
	}
	h, err := m.Provider.StartRun(ctx, worker, ws, provider.RunSpec{Task: m.task(decl)})
	if err != nil {
		return CohortData{}, fmt.Errorf("start run: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if closeErr := m.Provider.CloseRun(releaseCtx, h); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close run %s: %w", h.ID, closeErr))
		}
	}()

	if err := m.wait(ctx, h); err != nil {
		return CohortData{}, err
	}
	return m.collectData(ctx, h)
}

// instructions is the cohort worker's standing doctrine: answer as a plausible
// participant for the servable components only, and invent no demographics.
func (m *ModelCohort) instructions(decl instrument.Instrument) string {
	var b strings.Builder
	b.WriteString("You are a plausible participant in a study. Answer its trials as an ordinary person would, honestly and without inventing a fixed identity.\n\n")
	b.WriteString(m.Config.PromptTemplate)
	b.WriteString("\n\nYou have no assigned characteristics and must not invent any about yourself. Do not describe yourself; only respond to the trials.\n")
	b.WriteString("Cover only these servable components: ")
	b.WriteString(strings.Join(m.Coverage.Servable, ", "))
	b.WriteString(".\n")
	return b.String()
}

// task tells the cohort worker to emit conforming trial data.
func (m *ModelCohort) task(decl instrument.Instrument) string {
	cols := make([]string, len(decl.Analysis.Input.Columns))
	for i, c := range decl.Analysis.Input.Columns {
		cols[i] = c.Name
	}
	return fmt.Sprintf("Simulate %d participants answering the servable trials, and write their responses to %s as CSV with exactly these columns: %s. Record only the trial responses — no demographic columns.",
		m.N, CohortDataFile, strings.Join(cols, ", "))
}

func (m *ModelCohort) wait(ctx context.Context, h provider.RunHandle) error {
	interval := m.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timeout := m.timeout
	if timeout <= 0 {
		timeout = defaultRunnerTimeout
	}
	sleep := m.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	deadline := time.Now().Add(timeout)
	for {
		status, err := m.Provider.Poll(ctx, h)
		if err != nil {
			return fmt.Errorf("poll cohort run %s: %w", h.ID, err)
		}
		if status.State.Terminal() {
			m.usage = status.Usage
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("cohort run %s did not finish within %s", h.ID, timeout)
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
}

func (m *ModelCohort) collectData(ctx context.Context, h provider.RunHandle) (CohortData, error) {
	arts, err := m.Provider.ListArtifacts(ctx, h)
	if err != nil {
		return CohortData{}, fmt.Errorf("list cohort artifacts: %w", err)
	}
	for _, a := range arts {
		if a.Path != CohortDataFile {
			continue
		}
		body, err := m.Provider.FetchArtifact(ctx, h, a)
		if err != nil {
			return CohortData{}, fmt.Errorf("fetch %s: %w", CohortDataFile, err)
		}
		header, err := csv.NewReader(bytes.NewReader(body)).Read()
		if err != nil {
			return CohortData{}, fmt.Errorf("%s has no header: %w", CohortDataFile, err)
		}
		return CohortData{Header: header, CSV: body}, nil
	}
	return CohortData{}, fmt.Errorf("the cohort produced no %s", CohortDataFile)
}

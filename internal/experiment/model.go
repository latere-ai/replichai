// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/latere-ai/replichai/internal/runtimes"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/instrument"
)

// CohortDataFile is the conforming trial data a model cohort writes and the
// cohort reads back.
const CohortDataFile = "cohort.csv"

// ErrPersonaInPrompt reports a prompt template that assigns the cohort a
// demographic or persona attribute. Inventing a population and reporting how
// its strata differ is fabricated participant data, and it is refused outright
// (007c) rather than deferred.
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
// servable components, answering as instructed, over the existing provider
// contract with no new backend. Its only claim beyond the generator is how a
// plausible responder behaves, so GateModelCohort gates it on ground truth,
// and its data is a pilot and never evidence about people.
type ModelCohort struct {
	N          int
	Trials     int
	Controller *runtimes.Controller
	Config     PilotConfig
	Coverage   Coverage // the servable components to drive; the rest are skipped

	usage managed.Usage
}

// Usage reports what the last Collect consumed, for the pilot.
func (m *ModelCohort) Usage() managed.Usage { return m.usage }

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

	spec := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "best"},
		Instructions: m.instructions(), Task: m.task(decl),
		Sandbox:    managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkOpen}},
		Evaluation: nil,
	}
	hooks := runtimes.Hooks{Retain: func(retainCtx context.Context, evidence runtimes.EvidenceReader) error {
		m.usage = evidence.Status.Usage
		var retainErr error
		data, retainErr = m.collectData(retainCtx, evidence)
		return retainErr
	}}
	_, err = m.Controller.Execute(ctx, spec, hooks)
	return data, err
}

// instructions is the cohort worker's standing instructions: answer as a
// plausible participant for the servable components only, and invent no
// demographics.
func (m *ModelCohort) instructions() string {
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

func (m *ModelCohort) collectData(ctx context.Context, evidence runtimes.EvidenceReader) (CohortData, error) {
	for _, a := range evidence.Artifacts {
		if a.Path != CohortDataFile {
			continue
		}
		body, err := evidence.Fetch(ctx, a)
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

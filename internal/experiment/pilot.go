package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/provider"
)

// Caps bound what one pilot may cost, so a synthetic run cannot be silently
// billed to the single shared credential. A run exceeding any cap is refused
// before it starts.
type Caps struct {
	MaxParticipants int `json:"max_participants"`
	MaxTrials       int `json:"max_trials"`
	MaxCalls        int `json:"max_calls"` // participants * trials, the model-call budget
}

// Admit refuses a run whose size exceeds the caps, before it starts.
func (c Caps) Admit(participants, trials int) error {
	if c.MaxParticipants <= 0 || c.MaxTrials <= 0 || c.MaxCalls <= 0 {
		return errors.New("caps must be set before a pilot runs")
	}
	if participants > c.MaxParticipants {
		return fmt.Errorf("%d participants exceeds the cap of %d", participants, c.MaxParticipants)
	}
	if trials > c.MaxTrials {
		return fmt.Errorf("%d trials exceeds the cap of %d", trials, c.MaxTrials)
	}
	if participants*trials > c.MaxCalls {
		return fmt.Errorf("%d model calls exceeds the cap of %d", participants*trials, c.MaxCalls)
	}
	return nil
}

// PilotConfig is a pilot's own hidden decisions, recorded so the pilot is
// reproducible in exactly the way the audit's hidden-decisions list exists to
// expose. The platform holds itself to the standard it grades papers against.
type PilotConfig struct {
	N    int  `json:"n"`
	Caps Caps `json:"caps"`
	// Model-cohort configuration; empty for a generated cohort.
	Model          string  `json:"model,omitempty"`
	Snapshot       string  `json:"snapshot,omitempty"`
	PromptTemplate string  `json:"prompt_template,omitempty"`
	OrderingSeeds  []int64 `json:"ordering_seeds,omitempty"` // one per participant
	// Generated-cohort configuration.
	Seed int64 `json:"seed,omitempty"`
}

// Pilot is what a synthetic run emits. It is deliberately NOT an Outcome or a
// Result: the compiler, not a code review, is what stops a pilot from flowing
// into a path that reports findings about people (AC7). Kind is always set — a
// pilot with no cohort_kind is never written.
type Pilot struct {
	Kind        string                            `json:"cohort_kind"` // the indelible label
	Config      PilotConfig                       `json:"config"`
	Data        CohortData                        `json:"-"` // the bytes; exported separately
	Calibration *Comparison                       `json:"calibration,omitempty"`
	NotCovered  []instrument.ComponentServability `json:"not_covered,omitempty"`
	Usage       provider.Usage                    `json:"usage"`
}

// ErrIncompletePilot reports a pilot missing a decision it must record. Such a
// pilot is not written, because an unrecorded configuration is unreproducible.
var ErrIncompletePilot = errors.New("pilot is missing a required decision")

// validate refuses a pilot that has not recorded what it must (AC12). A model
// cohort must name its model, snapshot, prompt, and per-participant seeds; every
// pilot must have a positive N and caps.
func (p Pilot) validate() error {
	switch p.Kind {
	case CohortGenerated, CohortModel:
	default:
		return fmt.Errorf("%w: cohort_kind is %q", ErrIncompletePilot, p.Kind)
	}
	if p.Config.N <= 0 {
		return fmt.Errorf("%w: N is not recorded", ErrIncompletePilot)
	}
	if p.Config.Caps.MaxParticipants <= 0 || p.Config.Caps.MaxTrials <= 0 || p.Config.Caps.MaxCalls <= 0 {
		return fmt.Errorf("%w: caps are not recorded", ErrIncompletePilot)
	}
	if p.Kind == CohortModel {
		if p.Config.Model == "" || p.Config.Snapshot == "" || p.Config.PromptTemplate == "" {
			return fmt.Errorf("%w: a model cohort must record model, snapshot, and prompt", ErrIncompletePilot)
		}
		if len(p.Config.OrderingSeeds) != p.Config.N {
			return fmt.Errorf("%w: a model cohort must record one ordering seed per participant", ErrIncompletePilot)
		}
	}
	return nil
}

// Render produces PILOT.md — never RESULTS.md — which opens with what the pilot
// does NOT establish before it shows a single number (AC6), and carries the
// cohort_kind label. It returns an error rather than a report for an incomplete
// pilot, so an unreproducible pilot is simply not written.
func (p Pilot) Render() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# PILOT — %s\n\n", p.Kind)
	b.WriteString("**This is a pilot, not a study. It establishes nothing about people.** ")
	b.WriteString("It exists to shake down the apparatus and the analysis and to explore how the instrument behaves — every finding here is about the instrument, never about the paper and never about any person. ")
	b.WriteString("It does not estimate the paper's effect, stand in for the study, or power a sample-size calculation. No number below should be read as evidence about a population.\n\n")

	b.WriteString("## Configuration\n\n")
	fmt.Fprintf(&b, "- cohort_kind: `%s`\n", p.Kind)
	fmt.Fprintf(&b, "- participants (N): %d\n", p.Config.N)
	fmt.Fprintf(&b, "- caps: %d participants, %d trials, %d calls\n",
		p.Config.Caps.MaxParticipants, p.Config.Caps.MaxTrials, p.Config.Caps.MaxCalls)
	if p.Kind == CohortModel {
		fmt.Fprintf(&b, "- model: %s (snapshot %s)\n", p.Config.Model, p.Config.Snapshot)
		fmt.Fprintf(&b, "- prompt template: %q\n", p.Config.PromptTemplate)
		fmt.Fprintf(&b, "- per-participant ordering seeds: %d recorded\n", len(p.Config.OrderingSeeds))
	} else {
		fmt.Fprintf(&b, "- seed: %d (the run is reproducible from it)\n", p.Config.Seed)
	}
	fmt.Fprintf(&b, "- usage: %d input + %d output tokens\n\n", p.Usage.InputTokens, p.Usage.OutputTokens)

	if len(p.NotCovered) > 0 {
		b.WriteString("## Components not covered\n\n")
		b.WriteString("A model cohort cannot be driven through a component a page cannot serve, so these were skipped:\n\n")
		for _, c := range p.NotCovered {
			fmt.Fprintf(&b, "- **%s** — not servable (%s): %s\n",
				c.Component, strings.Join(c.Failed, ", "), c.Why)
		}
		b.WriteString("\n")
	}

	if p.Calibration != nil {
		b.WriteString("## Calibration\n\n")
		fmt.Fprintf(&b, "%s.\n\n", p.Calibration.Subject())
		b.WriteString("| metric | reported | cohort | delta |\n|---|---|---|---|\n")
		for _, r := range p.Calibration.Rows {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.Metric, r.Reported, r.Recomputed, r.Delta)
		}
	}
	return []byte(b.String()), nil
}

// Export is the machine-readable pilot for an API response or a data export. Its
// cohort_kind is a required, non-omitempty field, so no path out can strip the
// label.
func (p Pilot) Export() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

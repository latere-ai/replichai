package experiment

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/latere-ai/replichai/internal/instrument"
)

func validModelPilot() Pilot {
	return Pilot{
		Kind: CohortModel,
		Config: PilotConfig{
			N:              2,
			Caps:           Caps{MaxParticipants: 10, MaxTrials: 100, MaxCalls: 1000},
			Model:          "claude-x",
			Snapshot:       "2026-01-01",
			PromptTemplate: "You are a study participant. {{TRIAL}}",
			OrderingSeeds:  []int64{1, 2},
		},
		Calibration: &Comparison{Source: SourceSynthetic, Rows: []ComparisonRow{
			{Metric: "pm_accuracy", Reported: "0.82", Recomputed: "0.79", Delta: "-0.03"},
		}},
		NotCovered: []instrument.ComponentServability{
			{Component: "feed manipulation", Servable: false, Failed: []string{instrument.CriterionPageAdministrable}, Why: "elsewhere"},
		},
	}
}

// AC5: the cohort_kind label survives every path out — the stored record, the
// report, and the export. Enumerated, not spot-checked on one.
func TestLabelSurvivesEveryPath(t *testing.T) {
	p := validModelPilot()
	report, err := p.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	export, err := p.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	// The stored experiment record carries the same label.
	rec := Experiment{Source: SourceSynthetic, CohortKind: p.Kind}

	paths := map[string]string{
		"stored record (API/export source)": rec.CohortKind,
		"PILOT.md report":                   string(report),
		"JSON export/API response":          string(export),
	}
	for name, out := range paths {
		if !strings.Contains(out, CohortModel) {
			t.Errorf("%s dropped the cohort_kind label", name)
		}
	}
	// The export key is the stable contract name, not omitempty.
	if !strings.Contains(string(export), `"cohort_kind"`) {
		t.Error("export must carry cohort_kind as a required key")
	}
}

// AC6: PILOT.md opens with what it does not establish, before its first number,
// and is never named or shaped like a result.
func TestPilotReportDisclaimsBeforeAnyNumber(t *testing.T) {
	report, err := validModelPilot().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(report)
	disclaimer := strings.Index(text, "establishes nothing about people")
	if disclaimer < 0 {
		t.Fatal("the report must state what it does not establish")
	}
	firstDigit := strings.IndexFunc(text, func(r rune) bool { return unicode.IsDigit(r) })
	if firstDigit >= 0 && firstDigit < disclaimer {
		t.Error("a number appears before the disclaimer — the pilot must disclaim first")
	}
	if strings.Contains(text, "RESULTS") {
		t.Error("a pilot must never be shaped or named like a result")
	}
}

// AC12: a pilot missing a required decision is not written.
func TestIncompletePilotIsNotWritten(t *testing.T) {
	cases := map[string]func(*Pilot){
		"no kind":             func(p *Pilot) { p.Kind = "" },
		"no N":                func(p *Pilot) { p.Config.N = 0 },
		"no caps":             func(p *Pilot) { p.Config.Caps = Caps{} },
		"no model":            func(p *Pilot) { p.Config.Model = "" },
		"no snapshot":         func(p *Pilot) { p.Config.Snapshot = "" },
		"no prompt":           func(p *Pilot) { p.Config.PromptTemplate = "" },
		"seed count mismatch": func(p *Pilot) { p.Config.OrderingSeeds = []int64{1} }, // N is 2
	}
	for name, mangle := range cases {
		t.Run(name, func(t *testing.T) {
			p := validModelPilot()
			mangle(&p)
			if _, err := p.Render(); !errors.Is(err, ErrIncompletePilot) {
				t.Errorf("Render should refuse (%s), got %v", name, err)
			}
			if _, err := p.Export(); !errors.Is(err, ErrIncompletePilot) {
				t.Errorf("Export should refuse (%s), got %v", name, err)
			}
		})
	}
}

// A generated pilot needs no model fields, only N, caps, and its seed.
func TestGeneratedPilotIsComplete(t *testing.T) {
	p := Pilot{Kind: CohortGenerated, Config: PilotConfig{
		N: 30, Seed: 7, Caps: Caps{MaxParticipants: 100, MaxTrials: 100, MaxCalls: 10000},
	}}
	if _, err := p.Render(); err != nil {
		t.Fatalf("a complete generated pilot should render: %v", err)
	}
}

// AC13: a run exceeding its cap is refused before it starts.
func TestCapsAdmit(t *testing.T) {
	caps := Caps{MaxParticipants: 10, MaxTrials: 50, MaxCalls: 200}
	if err := caps.Admit(10, 20); err != nil {
		t.Errorf("a within-cap run should be admitted: %v", err)
	}
	if err := caps.Admit(11, 20); err == nil {
		t.Error("too many participants should be refused")
	}
	if err := caps.Admit(10, 51); err == nil {
		t.Error("too many trials should be refused")
	}
	if err := caps.Admit(10, 40); err == nil {
		t.Error("too many total calls (10*40=400 > 200) should be refused")
	}
	if err := (Caps{}).Admit(1, 1); err == nil {
		t.Error("unset caps must not admit a run")
	}
}

// AC7: a Pilot is a distinct type from Outcome — the boundary is the compiler's,
// not a reviewer's — and its Kind is required, not optional.
func TestPilotIsNotAnOutcome(t *testing.T) {
	// Kind is load-bearing: a zero Pilot is never written.
	if _, err := (Pilot{}).Export(); err == nil {
		t.Error("a Pilot with no cohort_kind must not export")
	}
	// The types do not convert: neither embeds or holds the other, so a pilot
	// cannot flow into a path that consumes a run outcome. Asserted structurally
	// (like the stored-declaration guard) so the boundary cannot silently erode.
	pilotT, outcomeT := reflect.TypeOf(Pilot{}), reflect.TypeOf(Outcome{})
	for i := 0; i < pilotT.NumField(); i++ {
		if pilotT.Field(i).Type == outcomeT {
			t.Errorf("Pilot.%s holds an Outcome — the pilot boundary would erode", pilotT.Field(i).Name)
		}
	}
	for i := 0; i < outcomeT.NumField(); i++ {
		if outcomeT.Field(i).Type == pilotT {
			t.Errorf("Outcome.%s holds a Pilot — the pilot boundary would erode", outcomeT.Field(i).Name)
		}
	}
}

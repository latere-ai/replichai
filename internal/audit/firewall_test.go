package audit_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/experiment"
	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider/memory"
)

// The firewall the whole 007 track rests on: the audit never reads an
// experiment. An auditor that could ingest experiment output could let a
// synthetic cohort inform a boundary classification or a rate without ever
// writing the verdict itself — the same violation by a side door. The missing
// import edge forbids it, and an import cycle would be a build failure; this is
// that same guarantee made explicit in the direction that matters (007 crit. 4).
func TestAuditNeverImportsExperiment(t *testing.T) {
	const (
		auditPkg      = "github.com/latere-ai/replichai/internal/audit"
		experimentPkg = "github.com/latere-ai/replichai/internal/experiment"
	)
	out, err := exec.Command("go", "list", "-deps", auditPkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", auditPkg, err)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		if dep == experimentPkg {
			t.Fatalf("internal/audit imports %s — the audit must never read an experiment", experimentPkg)
		}
	}
}

// AC4 (second half): re-auditing a paper after pilots have run against its
// instrument produces the same verdict. The audit does not read an experiment,
// so a thousand pilots or none leave its verdict identical. This lives in an
// external test package so it can import both audit and experiment without
// creating the production edge the test above forbids.
func TestReAuditIsUnaffectedByPilots(t *testing.T) {
	m := method.Method{
		Instructions: "Audit the paper. Write to {{OUTPUT_DIR}}.",
		Rubric:       "1. Verdict.",
		TaskTemplate: "Audit {{PAPER}}.",
	}
	auditor := audit.New(memory.New(), m, audit.WithPollInterval(0))

	first, err := auditor.Run(context.Background(), "arxiv:2302.03714", t.TempDir())
	if err != nil {
		t.Fatalf("first audit: %v", err)
	}

	// A generated pilot runs against an instrument of the same paper.
	decl := instrument.Instrument{Analysis: &instrument.Analysis{
		Command: "python a.py " + instrument.InputPlaceholder,
		Input:   instrument.Schema{Columns: []instrument.Column{{Name: "x", Type: "number", Required: true}}},
	}}
	if _, err := (experiment.Generated{N: 10, Seed: 1}).Collect(context.Background(), decl); err != nil {
		t.Fatalf("pilot: %v", err)
	}

	second, err := auditor.Run(context.Background(), "arxiv:2302.03714", t.TempDir())
	if err != nil {
		t.Fatalf("second audit: %v", err)
	}
	if first.Grade() != second.Grade() {
		t.Errorf("the audit verdict moved across a pilot: %s -> %s", first.Grade(), second.Grade())
	}
}

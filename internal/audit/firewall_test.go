package audit_test

import (
	"os/exec"
	"strings"
	"testing"
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
	for _, dep := range strings.Fields(string(out)) {
		if dep == experimentPkg {
			t.Fatalf("internal/audit imports %s — the audit must never read an experiment", experimentPkg)
		}
	}
}

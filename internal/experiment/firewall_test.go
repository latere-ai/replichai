package experiment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC10: verdict and rate in the 002 manifest are untouched by every path in this
// spec. The structural guarantee is that no experiment code constructs or writes
// a reproduction manifest — experiment imports publish for its Repo type, not to
// grade a reproduction. This scans the package source so a future change that
// wired an experiment into the manifest would trip here, in the direction the
// firewall cares about.
func TestExperimentNeverWritesAManifest(t *testing.T) {
	forbidden := []string{"ManifestFor", "publish.Manifest", "publish.Reproduction", "SchemaVersion"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, bad := range forbidden {
			if strings.Contains(string(body), bad) {
				t.Errorf("%s references %q — an experiment must never write a reproduction manifest", name, bad)
			}
		}
	}
}

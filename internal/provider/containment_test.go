package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vendorTerms are names that must not leak outside the backend adapter.
// Vendor-neutrality is a product requirement, not a style preference: the
// method, the evaluation cases, and every package above the seam describe
// roles, never a supplier.
//
// The refinement the concrete provider name introduces: the *interface* is
// neutral, but the *adapter* names its backend — including the public provider
// name it registers under (`anthropic`) and reports from Name(). That name is
// chosen inside the adapter and reaches the seam only as runtime data, never as
// a literal, so provider.go and the CLI stay term-free and this test still
// passes. The seam learns its default through RegisterDefault, not a constant.
var vendorTerms = []string{"anthropic", "claude", "managed agent", "managed-agent"}

// adapterDir is the one vendor-aware package. Every file in it may name the
// backend, so allowing it by directory rather than by file means splitting the
// adapter into more files — the Prompter, say — needs no allowlist edit.
const adapterDir = "internal/provider/hosted"

// allowed lists the non-adapter paths permitted to name a backend, and why.
var allowed = map[string]string{
	"go.mod":                                "the dependency manifest names what it depends on",
	"go.sum":                                "checksums mirror go.mod",
	"internal/provider/containment_test.go": "this test names the terms it looks for",
	"docs/architecture.md":                  "documents the go.mod tradeoff explicitly",
	"specs/004-go-port.md":                  "records the decision to adopt the SDK",
}

// TestOnlyTheAdapterNamesTheBackend walks the repository and fails if a
// vendor name appears anywhere it should not.
func TestOnlyTheAdapterNamesTheBackend(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			switch rel {
			// reference/ holds the second implementation, which has its own
			// adapter; .git and build output are not source.
			case ".git", "reference", "runs", "outputs":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(rel) {
		case ".go", ".md", ".mod", ".sum", ".json", ".yaml", ".yml":
		default:
			return nil
		}
		slash := filepath.ToSlash(rel)
		if slash == adapterDir || strings.HasPrefix(slash, adapterDir+"/") {
			return nil
		}
		if _, ok := allowed[slash]; ok {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(body))
		for _, term := range vendorTerms {
			if strings.Contains(lower, term) {
				offenders = append(offenders, filepath.ToSlash(rel)+" contains "+term)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	for _, offender := range offenders {
		t.Errorf("vendor name outside the adapter: %s", offender)
	}
}

// TestEveryAllowedPathStillExists keeps the allowlist honest — a stale entry
// would silently widen what this test permits.
func TestEveryAllowedPathStillExists(t *testing.T) {
	root := repoRoot(t)
	for rel := range allowed {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("allowlist names a path that no longer exists: %s", rel)
		}
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(adapterDir))); err != nil || !info.IsDir() {
		t.Errorf("adapterDir %q is not a directory: %v", adapterDir, err)
	}
}

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}

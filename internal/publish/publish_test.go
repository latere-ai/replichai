// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/provider"
)

var fixedClock = func() time.Time { return time.Date(2026, 7, 23, 9, 14, 0, 0, time.UTC) }

func resultWithFiles(t *testing.T, files map[string]string) audit.Result {
	t.Helper()
	dir := t.TempDir()
	var arts []provider.Artifact
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		arts = append(arts, provider.Artifact{Path: p})
	}
	return audit.Result{
		ArtifactDir:  dir,
		MethodDigest: "sha256:abc",
		Status:       provider.RunStatus{Verdict: &provider.Verdict{Grade: provider.GradePassed}},
		Artifacts:    arts,
	}
}

func planFrom(t *testing.T, files map[string]string) Plan {
	t.Helper()
	plan, err := PlanPublication(resultWithFiles(t, files), pleasQLarify(), "a3f9")
	if err != nil {
		t.Fatalf("PlanPublication: %v", err)
	}
	return plan
}

func repoPath(f *FSForge, name, rel string) string {
	return filepath.Join(f.Root, name, filepath.FromSlash(rel))
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// TestPublishPreviewCreatesNothing checks AC2: a preview, which is the
// default, creates nothing.
func TestPublishPreviewCreatesNothing(t *testing.T) {
	plan := planFrom(t, map[string]string{"README.md": "# r", "src/main.py": "x"})
	forge := NewFSForge(t.TempDir(), DefaultOrg)

	pub, err := Publish(context.Background(), plan, forge) // no Live()
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !pub.Preview {
		t.Error("a default publish must be a preview")
	}
	if ok, _ := forge.Exists(context.Background(), plan.RepoName); ok {
		t.Error("preview created a repository")
	}
}

// TestPublishLiveCreatesPrivateRepo checks AC3 and the success path: a live
// publish creates a private repository and pushes the manifest, the worker
// files, and EXCLUDED.md, and never the third-party file.
func TestPublishLiveCreatesPrivateRepo(t *testing.T) {
	plan := planFrom(t, map[string]string{
		"README.md":          "# reproduction",
		"src/main.py":        "print(1)",
		"data/benchmark.csv": "gated,data,here", // third-party, so quarantined
	})
	forge := NewFSForge(t.TempDir(), DefaultOrg)

	pub, err := Publish(context.Background(), plan, forge, Live(), WithClock(fixedClock))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if pub.Preview || pub.Existed {
		t.Errorf("first live publish: %+v", pub)
	}
	if pub.Repo.Owner != DefaultOrg || pub.Repo.Name != "pleasqlarify-visual-pragmatic-repair-a3f9" {
		t.Errorf("repo handle = %+v", pub.Repo)
	}
	if !forge.IsPrivate(plan.RepoName) {
		t.Error("AC3: the repository must be private")
	}

	// The worker files, the manifest, and EXCLUDED.md are present.
	for _, f := range []string{"README.md", "src/main.py", ManifestFile, ExcludedFile} {
		if !exists(repoPath(forge, plan.RepoName, f)) {
			t.Errorf("expected %s in the published repository", f)
		}
	}
	// The third-party file is not.
	if exists(repoPath(forge, plan.RepoName, "data/benchmark.csv")) {
		t.Error("a quarantined third-party file was published")
	}

	// The manifest's created was stamped from the injected clock.
	raw, err := os.ReadFile(repoPath(forge, plan.RepoName, ManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if !m.Reproduction.Created.Equal(fixedClock()) {
		t.Errorf("created = %v, want the injected clock %v", m.Reproduction.Created, fixedClock())
	}
}

// TestPublishIsIdempotentForTheSamePlan checks AC7 and AC8: republishing the
// same plan does not fork, and completes rather than creating a second
// repository.
func TestPublishIsIdempotentForTheSamePlan(t *testing.T) {
	plan := planFrom(t, map[string]string{"README.md": "# r", "src/main.py": "x"})
	forge := NewFSForge(t.TempDir(), DefaultOrg)
	ctx := context.Background()

	first, err := Publish(ctx, plan, forge, Live())
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if first.Existed {
		t.Error("first publish should not report an existing repo")
	}
	second, err := Publish(ctx, plan, forge, Live())
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if !second.Existed {
		t.Error("AC7: the second publish must see the repository already exists")
	}

	// Exactly one repository directory under the forge root.
	entries, err := os.ReadDir(forge.Root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("republishing forked the repository: %d directories", len(entries))
	}
}

// TestPublishAbortsOnSecretInContent checks that the content sweep is the
// backstop: a secret under an innocent worker filename aborts the publish
// before anything is created.
func TestPublishAbortsOnSecretInContent(t *testing.T) {
	plan := planFrom(t, map[string]string{
		"README.md":   "# r",
		"src/conf.py": "TOKEN = \"AKIAIOSFODNN7EXAMPLE\"",
	})
	forge := NewFSForge(t.TempDir(), DefaultOrg)

	_, err := Publish(context.Background(), plan, forge, Live())
	if !errors.Is(err, ErrSecretInContent) {
		t.Fatalf("err = %v, want ErrSecretInContent", err)
	}
	if ok, _ := forge.Exists(context.Background(), plan.RepoName); ok {
		t.Error("a secret abort must leave no repository behind")
	}
}

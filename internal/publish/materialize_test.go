// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeMakesOneCommitOfTheFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspace")
	files := map[string][]byte{"README.md": []byte("# r\n"), "src/run.py": []byte("print(5)\n")}
	if err := Materialize(context.Background(), dir, files, "the work, as the session left it"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "log", "--format=%an %s").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "replichai the work, as the session left it" {
		t.Fatalf("log = %q, %v", out, err)
	}
	tree, _ := exec.Command("git", "-C", dir, "ls-files").CombinedOutput()
	if strings.Fields(string(tree))[0] != "README.md" || !strings.Contains(string(tree), "src/run.py") {
		t.Fatalf("tree = %q", tree)
	}
	push := LocalPush{Workspace: dir}
	if count, err := push.git(context.Background(), "rev-list", "--count", "HEAD"); err != nil || strings.TrimSpace(count) != "1" {
		t.Fatalf("commits = %q, %v", count, err)
	}
}

func TestMaterializeRefusesAPathOutside(t *testing.T) {
	for _, name := range []string{"../escape", "/abs", ".git/config", "a/../../b", "."} {
		dir := filepath.Join(t.TempDir(), "workspace")
		if err := Materialize(context.Background(), dir, map[string][]byte{name: []byte("x")}, "m"); err == nil {
			t.Errorf("%q was written", name)
		}
	}
	existing := t.TempDir()
	if err := Materialize(context.Background(), existing, nil, "m"); !errors.Is(err, ErrWorkspaceExists) {
		t.Fatalf("an existing directory = %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Materialize(context.Background(), filepath.Join(blocked, "workspace"), nil, "m"); err == nil {
		t.Fatal("a workspace under a file was made")
	}
}

// A run's workspace is synced snapshot by snapshot: the first creates the
// repository, an unchanged snapshot commits nothing, and a changed one
// commits on top, removing what the session removed, so each push of it is a
// fast-forward of the last.
func TestSyncWorkspaceCommitsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "workspace")
	log := func() string {
		out, err := exec.Command("git", "-C", dir, "log", "--format=%s").CombinedOutput()
		if err != nil {
			t.Fatalf("log: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if changed, err := SyncWorkspace(ctx, dir, map[string][]byte{}, "empty"); err != nil || changed {
		t.Fatalf("an empty first snapshot = %v, %v; it should commit nothing", changed, err)
	}
	first := map[string][]byte{"notes.md": []byte("one\n"), "src/a.py": []byte("a\n")}
	if changed, err := SyncWorkspace(ctx, dir, first, "first"); err != nil || !changed || log() != "first" {
		t.Fatalf("the first snapshot = %v, %v, log %q", changed, err, log())
	}
	if changed, err := SyncWorkspace(ctx, dir, first, "again"); err != nil || changed || log() != "first" {
		t.Fatalf("an unchanged snapshot = %v, %v, log %q", changed, err, log())
	}
	second := map[string][]byte{"notes.md": []byte("two\n")}
	if changed, err := SyncWorkspace(ctx, dir, second, "second"); err != nil || !changed || log() != "second\nfirst" {
		t.Fatalf("a changed snapshot = %v, %v, log %q", changed, err, log())
	}
	tree, _ := exec.Command("git", "-C", dir, "ls-files").CombinedOutput()
	if strings.TrimSpace(string(tree)) != "notes.md" {
		t.Fatalf("the tree after src/a.py was removed is %q", tree)
	}
	if _, err := SyncWorkspace(ctx, dir, map[string][]byte{"../escape": []byte("x")}, "m"); err == nil {
		t.Fatal("a path outside the workspace was written")
	}
}

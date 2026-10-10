// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/latere-ai/replichai/internal/publish"
)

func writeRepo(t *testing.T, root, name string, body []byte) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, publish.ManifestFile), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFSSourceReadsAndParses(t *testing.T) {
	root := t.TempDir()
	good, _ := json.Marshal(manifestOf("repo-a", "10.1145/x", "a-paper", t1))
	writeRepo(t, root, "repo-a", good)
	writeRepo(t, root, "repo-bad", []byte("{not json"))

	src := NewFSSource(root)
	names, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("want 2 repos, got %v", names)
	}

	m, err := src.Manifest(context.Background(), "repo-a")
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if m.Paper.Key != "10.1145/x" {
		t.Errorf("parsed manifest = %+v", m.Paper)
	}
	if _, err := src.Manifest(context.Background(), "repo-bad"); err == nil {
		t.Error("a malformed manifest should error (the registry then skips it)")
	}

	// End to end through the registry: the good repo is found, the bad skipped.
	r := New(src, WithWarner(func(string) {}))
	priors, err := r.Lookup(context.Background(), doiID("10.1145/x"))
	if err != nil || len(priors) != 1 {
		t.Errorf("Lookup over FSSource: %d priors, err=%v", len(priors), err)
	}
}

// TestFSSourceAbsentManifestIsNoManifest checks that a repository with no
// manifest yields ErrNoManifest and a malformed one does not. The first is a
// run that has not finalized, the second is a read that failed. The publish
// gate reports the two differently, so the distinction has to be made here
// rather than inferred from an error string.
func TestFSSourceAbsentManifestIsNoManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "unfinished"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRepo(t, root, "repo-bad", []byte("{not json"))
	src := NewFSSource(root)

	if _, err := src.Manifest(context.Background(), "unfinished"); !errors.Is(err, ErrNoManifest) {
		t.Errorf("no manifest: err = %v, want ErrNoManifest", err)
	}
	if _, err := src.Manifest(context.Background(), "repo-bad"); err == nil || errors.Is(err, ErrNoManifest) {
		t.Errorf("malformed manifest: err = %v, want a read failure", err)
	}
}

func TestFSSourceMissingRootIsEmpty(t *testing.T) {
	src := NewFSSource(filepath.Join(t.TempDir(), "does-not-exist"))
	names, err := src.List(context.Background())
	if err != nil || len(names) != 0 {
		t.Errorf("a missing root should list nothing without error, got %v err=%v", names, err)
	}
}

// TestFSSourceFile checks that File reads any committed artifact, such as the
// transcript (005/006h), and reports an absent file as a miss, not an error.
func TestFSSourceFile(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "repo-a", []byte("{}"))
	if err := os.WriteFile(filepath.Join(root, "repo-a", "transcript.jsonl"), []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewFSSource(root)

	raw, ok, err := src.File(context.Background(), "repo-a", "transcript.jsonl")
	if err != nil || !ok || string(raw) != "line\n" {
		t.Fatalf("File present = %q ok=%v err=%v", raw, ok, err)
	}
	if _, ok, err := src.File(context.Background(), "repo-a", "absent.txt"); ok || err != nil {
		t.Errorf("absent file: ok=%v err=%v; want false, nil", ok, err)
	}
}

// TestAManifestReadsWithOrWithoutARunRecord checks both shapes the registry
// meets. Every manifest published before the run record has none, and those
// have to keep reading; one written since carries it, and the registry hands
// it on untouched.
func TestAManifestReadsWithOrWithoutARunRecord(t *testing.T) {
	root := t.TempDir()
	// A manifest as it was written before the record existed, verbatim.
	writeRepo(t, root, "before", []byte(`{
	  "schema": 1,
	  "paper": {"key": "10.1145/x", "scheme": "doi", "title": "A Paper", "slug": "a-paper"},
	  "reproduction": {"repo": "open-hai/before", "created": "2026-09-11T20:12:43Z", "method_digest": "sha256:f9af", "verdict": "passed"},
	  "redistribution": {"third_party_data": false, "review": "clear"},
	  "findings": {"present": true, "reproduction": true}
	}`))
	with := manifestOf("after", "10.1145/x", "a-paper", t1)
	with.Run = &publish.Run{Project: "chi-2026-audit", Run: 2, Runs: 3, Harness: "codex", State: "done", Rounds: 2}
	body, err := json.Marshal(with)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeRepo(t, root, "after", body)

	src := NewFSSource(root)
	before, err := src.Manifest(context.Background(), "before")
	if err != nil {
		t.Fatalf("a manifest with no run record does not read: %v", err)
	}
	if before.Run != nil || before.Reproduction.MethodDigest != "sha256:f9af" {
		t.Errorf("manifest without a run record = %+v", before)
	}
	after, err := src.Manifest(context.Background(), "after")
	if err != nil {
		t.Fatalf("a manifest with a run record does not read: %v", err)
	}
	if after.Run == nil || after.Run.Harness != "codex" || after.Run.Run != 2 || after.Run.Rounds != 2 {
		t.Errorf("run record read back as %+v", after.Run)
	}

	// Both are still reproductions of the same paper to the registry.
	priors, err := New(src, WithWarner(func(string) {})).Lookup(context.Background(), doiID("10.1145/x"))
	if err != nil || len(priors) != 2 {
		t.Errorf("Lookup found %d reproductions, err=%v, want both", len(priors), err)
	}
}

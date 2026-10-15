// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/paper"
)

// A run handed the study's copy of the paper gets it as a read-only input, and
// its instructions name where it is.
func TestARunIsHandedTheStudysCopyOfThePaper(t *testing.T) {
	runtime := managedmemory.NewWithStore(nil, managedmemory.Config{InputDir: "/inputs"})
	copied := paper.CopyOf([]byte("%PDF-1.7 the paper"), "https://arxiv.org/pdf/2605.19151")
	auditor := NewSessionAuditor(sessionController(t, runtime), testMethod, WithPaperCopy(copied))
	if _, err := auditor.Run(context.Background(), "arXiv:2605.19151", t.TempDir()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := runtime.Specs()[0]
	if len(spec.Resources) != 1 || spec.Resources[0].Kind != managed.ResourceInput {
		t.Fatalf("resources = %+v, want the paper as an input", spec.Resources)
	}
	input := spec.Resources[0].Input
	if input.Path != "paper.pdf" || input.SHA256 != copied.SHA256 || string(input.Content) != string(copied.Content) {
		t.Fatalf("input = %+v", input)
	}
	if !strings.Contains(spec.Instructions, "/inputs/paper.pdf") || !strings.Contains(spec.Instructions, "The paper you were given") {
		t.Fatalf("the instructions do not say where the paper is:\n%s", spec.Instructions)
	}
}

// A runtime that cannot mount an input read-only refuses the run before it
// starts, rather than running it on a copy of its own finding.
func TestARuntimeWithNoInputsCannotRunAStudy(t *testing.T) {
	runtime := managedmemory.New()
	copied := paper.CopyOf([]byte("%PDF-1.7"), "paper.pdf")
	auditor := NewSessionAuditor(sessionController(t, runtime), testMethod, WithPaperCopy(copied))
	if _, err := auditor.Run(context.Background(), "paper", t.TempDir()); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("Run = %v, want unsupported", err)
	}
	if len(runtime.Specs()) != 0 {
		t.Fatal("a session started without the paper it was to be given")
	}
}

// A named profile is enforced or refused, never swapped for whatever the
// runtime allows.
func TestANamedNetworkProfileIsEnforcedOrRefused(t *testing.T) {
	runtime := managedmemory.New()
	for _, profile := range NetworkProfiles() {
		auditor := NewSessionAuditor(sessionController(t, runtime), testMethod, WithNetworkProfile(profile))
		if _, err := auditor.Run(context.Background(), "paper", t.TempDir()); err != nil {
			t.Fatalf("Run under the %s profile: %v", profile, err)
		}
	}
	specs := runtime.Specs()
	if specs[0].Sandbox.Network.Mode != managed.NetworkAllowlist || specs[1].Sandbox.Network.Mode != managed.NetworkOpen {
		t.Fatalf("the profiles ran as %q and %q", specs[0].Sandbox.Network.Mode, specs[1].Sandbox.Network.Mode)
	}
	// A runtime that offers open egress and no allowlist, as a hosted one does.
	openOnly := capabilityRuntime{Runtime: managedmemory.New(), capabilities: managed.Capabilities{
		OutputDir:    managedmemory.OutputDir,
		NetworkModes: map[managed.NetworkMode]bool{managed.NetworkNone: true, managed.NetworkOpen: true},
	}}
	bounded := NewSessionAuditor(sessionController(t, openOnly), testMethod, WithNetworkProfile(ProfileArtifacts))
	_, err := bounded.Run(context.Background(), "paper", t.TempDir())
	if !errors.Is(err, managed.ErrUnsupported) || !strings.Contains(err.Error(), "artifacts") {
		t.Fatalf("Run under a profile the runtime cannot enforce = %v", err)
	}
}

func TestTheArtifactsProfileIsTheArtifactAllowlist(t *testing.T) {
	policy := ProfileArtifacts.Policy()
	if policy.Mode != managed.NetworkAllowlist || !slices.Equal(policy.AllowedDomains, ArtifactDomains()) {
		t.Fatalf("policy = %+v", policy)
	}
	if DefaultNetworkProfile != ProfileArtifacts {
		t.Errorf("the default profile is %q", DefaultNetworkProfile)
	}
}

func TestParseNetworkProfile(t *testing.T) {
	for name, want := range map[string]NetworkProfile{"artifacts": ProfileArtifacts, " Open ": ProfileOpen} {
		if got, err := ParseNetworkProfile(name); err != nil || got != want {
			t.Errorf("ParseNetworkProfile(%q) = %q, %v", name, got, err)
		}
	}
	if _, err := ParseNetworkProfile("anything"); err == nil || !strings.Contains(err.Error(), "artifacts, open") {
		t.Errorf("an unknown profile = %v, want the known ones listed", err)
	}
}

func TestThePaperCopyIsNamedForItsKind(t *testing.T) {
	for source, want := range map[string]string{
		"https://example.org/a.html":         "paper.html",
		"/home/someone/papers/kay.txt":       "paper.txt",
		"https://example.org/view?id=1&x=.y": "paper",
		"https://example.org/view":           "paper",
		"notes.markdownfile":                 "paper",
	} {
		if got := paperFileName(paper.CopyOf([]byte("text"), source)); got != want {
			t.Errorf("a copy from %s is named %q, want %q", source, got, want)
		}
	}
	if got := paperFileName(paper.CopyOf([]byte("%PDF-1.7"), "kay.bin")); got != "paper.pdf" {
		t.Errorf("a PDF is named %q", got)
	}
}

// A run of a study is handed the snapshots of what the authors released as
// read-only inputs, each under a name of its own, and its instructions say
// where each is and how to use it.
func TestARunIsHandedTheStudysArtifactSnapshots(t *testing.T) {
	runtime := managedmemory.NewWithStore(nil, managedmemory.Config{InputDir: "/inputs"})
	snapshots := []paper.Artifact{
		paper.SnapshotOf("https://github.com/authors/system", paper.ArtifactGit, "c0ffee", "system.bundle", []byte("bundle")),
		paper.SnapshotOf("https://gitlab.com/lab/system", paper.ArtifactGit, "beef", "system.bundle", []byte("other bundle")),
		paper.SnapshotOf("https://osf.io/abcd/download", paper.ArtifactFile, "", "", []byte("data")),
	}
	auditor := NewSessionAuditor(sessionController(t, runtime), testMethod, WithArtifacts(snapshots))
	if _, err := auditor.Run(context.Background(), "paper", t.TempDir()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := runtime.Specs()[0]
	var paths []string
	for _, resource := range spec.Resources {
		if resource.Kind != managed.ResourceInput {
			t.Fatalf("resource = %+v, want an input", resource)
		}
		paths = append(paths, resource.Input.Path)
	}
	if !slices.Equal(paths, []string{"artifacts/system.bundle", "artifacts/2-system.bundle", "artifacts/3-artifact"}) {
		t.Fatalf("paths = %v", paths)
	}
	for _, want := range []string{
		"The released materials you were given",
		`git clone /inputs/artifacts/system.bundle "$TMPDIR/system"`, "commit c0ffee",
		"https://osf.io/abcd/download: the file as it was downloaded, at /inputs/artifacts/3-artifact",
		`"released" list of verdict.json`,
	} {
		if !strings.Contains(spec.Instructions, want) {
			t.Errorf("the instructions do not say %q:\n%s", want, spec.Instructions)
		}
	}

	// A runtime that cannot mount an input read-only refuses the run.
	refused := NewSessionAuditor(sessionController(t, managedmemory.New()), testMethod, WithArtifacts(snapshots))
	if _, err := refused.Run(context.Background(), "paper", t.TempDir()); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("Run on a runtime with no inputs = %v", err)
	}
}

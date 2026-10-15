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
// runtime allows. The memory runtime offers open egress and no allowlist.
func TestANamedNetworkProfileIsEnforcedOrRefused(t *testing.T) {
	runtime := managedmemory.New()
	open := NewSessionAuditor(sessionController(t, runtime), testMethod, WithNetworkProfile(ProfileOpen))
	if _, err := open.Run(context.Background(), "paper", t.TempDir()); err != nil {
		t.Fatalf("Run under the open profile: %v", err)
	}
	if got := runtime.Specs()[0].Sandbox.Network.Mode; got != managed.NetworkOpen {
		t.Fatalf("the open profile ran as %q", got)
	}
	bounded := NewSessionAuditor(sessionController(t, managedmemory.New()), testMethod, WithNetworkProfile(ProfileArtifacts))
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

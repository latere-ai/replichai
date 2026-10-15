// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
)

// paperInput is the input a study hands every run: one file, carried with its
// digest.
func paperInput(body []byte) managed.Resource {
	sum := sha256.Sum256(body)
	return managed.Resource{Kind: managed.ResourceInput, Input: &managed.InputResource{
		Path: "paper.pdf", MediaType: "application/pdf", Content: body, SHA256: hex.EncodeToString(sum[:]),
	}}
}

// The input directory is named with the workspace, for the same session and
// beside it, so the instructions can name a paper's path before the session
// exists.
func TestTheInputDirectoryIsTheSessionsOwn(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if capabilities.InputDir == "" || filepath.Dir(capabilities.InputDir) != filepath.Dir(capabilities.OutputDir) {
		t.Fatalf("input dir %q is not beside the workspace %q", capabilities.InputDir, capabilities.OutputDir)
	}
}

// An input is placed outside the workspace, where the agent cannot commit it,
// and every stage that runs an agent reads it and cannot write it.
func TestAnInputIsReadOnlyAndOutsideTheWorkspace(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	body := bytes.Repeat([]byte("%PDF-1.7 "), 1024)
	s := spec(true)
	s.Instructions = "Write to " + capabilities.OutputDir + "/ and read " + capabilities.InputDir + "/paper.pdf."
	s.Resources = []managed.Resource{paperInput(body)}
	ref, err := runtime.Start(context.Background(), s)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	runToTerminal(t, runtime, ref)

	dir := sessionDir(t, ref)
	inputs := filepath.Join(dir, "inputs")
	if inputs != capabilities.InputDir {
		t.Fatalf("the session placed its inputs at %s, not at the %s it was told", inputs, capabilities.InputDir)
	}
	written, err := os.ReadFile(filepath.Join(inputs, "paper.pdf"))
	if err != nil || !bytes.Equal(written, body) {
		t.Fatalf("the input was not written intact: %v", err)
	}
	if info, err := os.Stat(filepath.Join(inputs, "paper.pdf")); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("the input is writable: %v %v", info.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "paper.pdf")); !os.IsNotExist(err) {
		t.Fatalf("the input reached the workspace: %v", err)
	}
	for _, role := range []string{"worker", "judge"} {
		stage := stageNamed(t, driver, role)
		mounted := false
		for _, path := range stage.Paths {
			if path.Host == inputs {
				mounted = true
				if path.Access != hostsandbox.ReadOnly || path.Destination() != inputs {
					t.Fatalf("the %s sees its inputs as %+v", role, path)
				}
			}
		}
		if !mounted {
			t.Fatalf("the %s was not given its inputs: %+v", role, stage.Paths)
		}
	}
	// The bytes are not kept a second time in the specification polled from.
	persisted, err := os.ReadFile(filepath.Join(dir, "session", "spec.json"))
	if err != nil {
		t.Fatalf("read the persisted spec: %v", err)
	}
	if bytes.Contains(persisted, []byte(base64.StdEncoding.EncodeToString(body))) {
		t.Fatal("the input's bytes are kept in the persisted spec")
	}
	if !strings.Contains(string(persisted), paperInput(body).Input.SHA256) {
		t.Error("the persisted spec dropped the input's digest with its bytes")
	}
}

// An input whose path escapes its directory is refused before the session
// exists, and leaves nothing behind.
func TestAnInputCannotEscapeItsDirectory(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	s := spec(false)
	input := paperInput([]byte("%PDF"))
	input.Input.Path = "../workspace/paper.pdf"
	s.Resources = []managed.Resource{input}
	if _, err := runtime.Start(context.Background(), s); err == nil {
		t.Fatal("an input outside its directory was accepted")
	}
}

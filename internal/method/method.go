// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package method loads the audit method from disk.
//
// The method is data, not code: the worker's standing instructions, the rubric
// it is graded against, and the task template a paper reference is substituted
// into. Keeping the rubric in a file means sharpening it costs a text edit
// rather than a redefinition of the worker.
package method

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// PaperPlaceholder is replaced with the paper reference in the task.
	PaperPlaceholder = "{{PAPER}}"
	// OutputDirPlaceholder is replaced with the backend's output location.
	OutputDirPlaceholder = "{{OUTPUT_DIR}}"
)

// ErrInvalid reports a missing or malformed method directory.
var ErrInvalid = errors.New("invalid method")

// Method is the three documents that define an audit.
type Method struct {
	Instructions string
	Rubric       string
	TaskTemplate string
}

// Digest pins which version of the method produced a reproduction, as
// "sha256:<hex>". Two reproductions are only comparable if their digests match.
// The three documents are framed with length-prefixed labels so no edit that
// moves a byte across the boundary between them can go unnoticed.
func (m Method) Digest() string {
	h := sha256.New()
	for _, part := range []struct{ name, text string }{
		{"instructions", m.Instructions},
		{"rubric", m.Rubric},
		{"task-template", m.TaskTemplate},
	} {
		// hash.Hash.Write never returns an error, so neither can this.
		_, _ = fmt.Fprintf(h, "%s:%d:", part.name, len(part.text))
		h.Write([]byte(part.text))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// TaskFor renders the task for one paper reference.
func (m Method) TaskFor(paper string) (string, error) {
	paper = strings.TrimSpace(paper)
	if paper == "" {
		return "", fmt.Errorf("%w: paper reference is empty", ErrInvalid)
	}
	if !strings.Contains(m.TaskTemplate, PaperPlaceholder) {
		return "", fmt.Errorf("%w: task template does not contain %s",
			ErrInvalid, PaperPlaceholder)
	}
	return strings.ReplaceAll(m.TaskTemplate, PaperPlaceholder, paper), nil
}

// InstructionsFor renders the instructions against one backend's output location.
func (m Method) InstructionsFor(outputDir string) string {
	return strings.ReplaceAll(m.Instructions, OutputDirPlaceholder, outputDir)
}

// Load reads the method from dir. An empty dir searches upward from the working
// directory for a "method" directory, so the CLI works from anywhere in the repo.
func Load(dir string) (Method, error) {
	if dir == "" {
		found, err := discover()
		if err != nil {
			return Method{}, err
		}
		dir = found
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Method{}, fmt.Errorf("%w: method directory not found: %s", ErrInvalid, dir)
	}

	read := func(name string) (string, error) {
		// The directory is a CLI flag or the repository's own layout, not
		// untrusted input; the file names are fixed constants.
		raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: fixed names under an operator-supplied directory
		if err != nil {
			return "", fmt.Errorf("%w: method directory is missing %s: %s", ErrInvalid, name, dir)
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return "", fmt.Errorf("%w: method file is empty: %s", ErrInvalid, name)
		}
		return text, nil
	}

	var m Method
	if m.Instructions, err = read("instructions.md"); err != nil {
		return Method{}, err
	}
	if m.Rubric, err = read("rubric.md"); err != nil {
		return Method{}, err
	}
	if m.TaskTemplate, err = read("task-template.md"); err != nil {
		return Method{}, err
	}
	return m, nil
}

// LoadExperiment reads the experiment runner's instructions (experiment.md)
// from the method directory. Like Load, an empty dir discovers the method
// directory by walking up from the working directory.
//
// It is separate from Load and outside Digest because the experiment worker
// (007b) is graded by nothing — it runs a declared command and reports what
// happened — so it needs no rubric or task template, and editing its doctrine
// must not change an audit's method digest.
func LoadExperiment(dir string) (string, error) {
	if dir == "" {
		found, err := discover()
		if err != nil {
			return "", err
		}
		dir = found
	}
	raw, err := os.ReadFile(filepath.Join(dir, "experiment.md")) //nolint:gosec // G304: fixed name under an operator-supplied directory
	if err != nil {
		return "", fmt.Errorf("%w: method directory is missing experiment.md: %s", ErrInvalid, dir)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", fmt.Errorf("%w: method file is empty: experiment.md", ErrInvalid)
	}
	return text, nil
}

// discover walks up from the working directory looking for a method directory.
//
// A candidate must actually contain the method, not merely be named "method" —
// this repository has a Go package at internal/method, and matching on the name
// alone would resolve to it from anywhere inside internal/.
func discover() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	for {
		candidate := filepath.Join(dir, "method")
		if isMethodDir(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w: no method directory found above the working directory", ErrInvalid)
		}
		dir = parent
	}
}

// isMethodDir reports whether dir holds the method's documents.
func isMethodDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	for _, name := range []string{"instructions.md", "rubric.md", "task-template.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

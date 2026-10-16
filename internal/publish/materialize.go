// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ErrWorkspaceExists reports a directory Materialize will not write into,
// because it already holds something and the push would carry it along.
var ErrWorkspaceExists = errors.New("the workspace directory already exists")

// Materialize writes a hosted session's files into dir as a new git
// repository with one commit and no remote, so the host can push the work of
// a runtime that keeps no repository of its own the way it pushes a local
// session's (045g). A hosted session's history is not on the record: the one
// commit says so, and is the host's, not the agent's. Every path is checked
// to stay inside dir.
func Materialize(ctx context.Context, dir string, files map[string][]byte, message string) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%w: %s", ErrWorkspaceExists, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		clean := path.Clean(name)
		if clean != name || clean == "." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return fmt.Errorf("refusing to write %q outside the workspace", name)
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, files[name], 0o600); err != nil {
			return err
		}
	}
	steps := [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.name", "replichai"},
		{"config", "user.email", "reproduction@replichai.invalid"},
		{"config", "commit.gpgsign", "false"},
		{"add", "--all"},
		{"commit", "--allow-empty", "--message", message},
	}
	for _, args := range steps {
		//nolint:gosec // a fixed git verb over this package's own arguments
		command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

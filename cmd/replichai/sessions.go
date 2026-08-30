// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// runSessions lists and removes the session directories a local run leaves
// behind (045a).
//
// Retention defaults to keeping them, because the workspace is the
// reproduction and deleting it on success would destroy the only copy of
// anything the push has not taken. That default makes them accumulate, so
// there has to be a way to reclaim the space that is not `rm -rf`.
func runSessions(_ context.Context, args []string, stdout, stderr *os.File) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: replichai sessions ls | replichai sessions prune [--older-than 7d] [--dry-run]")
		return errExitQuiet
	}
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("session-root", envOr("REPLICHAI_SESSION_ROOT", defaultSessionRoot()), "where session directories are made")
	olderThan := fs.Duration("older-than", 7*24*time.Hour, "with prune: only remove sessions older than this")
	dryRun := fs.Bool("dry-run", false, "with prune: say what would go, and remove nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return errExitQuiet
	}

	found, err := sessionDirs(*root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "ls":
		for _, entry := range found {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", entry.name,
				entry.modified.Format(time.RFC3339), sizeOf(entry.path))
		}
		if len(found) == 0 {
			fmt.Fprintf(stderr, "no sessions under %s\n", *root)
		}
		return nil
	case "prune":
		cutoff := time.Now().Add(-*olderThan)
		removed := 0
		for _, entry := range found {
			if entry.modified.After(cutoff) {
				continue
			}
			if *dryRun {
				fmt.Fprintf(stdout, "would remove %s\n", entry.path)
				removed++
				continue
			}
			if err := os.RemoveAll(entry.path); err != nil {
				fmt.Fprintf(stderr, "  ! %s: %v\n", entry.name, err)
				continue
			}
			fmt.Fprintf(stdout, "removed %s\n", entry.name)
			removed++
		}
		fmt.Fprintf(stderr, "%d of %d session(s) older than %s\n", removed, len(found), *olderThan)
		return nil
	default:
		fmt.Fprintf(stderr, "unknown sessions command %q\n", args[0])
		return errExitQuiet
	}
}

type sessionDir struct {
	name     string
	path     string
	modified time.Time
}

// sessionDirs lists what is under the session root, newest first. A root that
// does not exist is no sessions rather than an error: a machine that has never
// run one locally is not broken.
func sessionDirs(root string) ([]sessionDir, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var found []sessionDir
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		found = append(found, sessionDir{name: entry.Name(), path: filepath.Join(root, entry.Name()), modified: info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].modified.After(found[j].modified) })
	return found, nil
}

// sizeOf is what a session is holding, rendered for a person deciding whether
// to reclaim it. An unreadable directory reports nothing rather than failing a
// listing.
func sizeOf(path string) string {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // an unreadable entry contributes nothing; keep walking
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	switch {
	case total > 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(total)/(1<<30))
	case total > 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(total)/(1<<20))
	default:
		return fmt.Sprintf("%d KiB", total/(1<<10))
	}
}

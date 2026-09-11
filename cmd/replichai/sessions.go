// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
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
// this command exists to reclaim the space without a manual `rm -rf`.
func runSessions(_ context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("sessions", stderr)
	root := fs.String("session-root", envOr("REPLICHAI_SESSION_ROOT", defaultSessionRoot()), "directory where session directories are created")
	olderThan := fs.Duration("older-than", 7*24*time.Hour, "with prune: only remove sessions older than this")
	dryRun := fs.Bool("dry-run", false, "with prune: list what would be removed and remove nothing")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: replichai sessions ls | replichai sessions prune [--older-than 168h] [--dry-run]")
		return errExitQuiet
	}

	found, err := sessionDirs(*root)
	if err != nil {
		return err
	}
	switch positional[0] {
	case "ls":
		for _, entry := range found {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", entry.name,
				entry.modified.Format(time.RFC3339), sizeOf(entry.path))
		}
		if len(found) == 0 {
			_, _ = fmt.Fprintf(stderr, "no sessions under %s\n", *root)
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
				_, _ = fmt.Fprintf(stdout, "would remove %s\n", entry.path)
				removed++
				continue
			}
			if err := os.RemoveAll(entry.path); err != nil {
				_, _ = fmt.Fprintf(stderr, "  ! %s: %v\n", entry.name, err)
				continue
			}
			_, _ = fmt.Fprintf(stdout, "removed %s\n", entry.name)
			removed++
		}
		verb := "removed"
		if *dryRun {
			verb = "would be removed"
		}
		_, _ = fmt.Fprintf(stderr, "%d of %d session(s) %s (older than %s)\n", removed, len(found), verb, *olderThan)
		return nil
	default:
		_, _ = fmt.Fprintf(stderr, "unknown sessions command %q\n", positional[0])
		return errExitQuiet
	}
}

type sessionDir struct {
	name     string
	path     string
	modified time.Time
}

// sessionDirs lists the directories under the session root, newest first. A
// root that does not exist yields no sessions rather than an error, because a
// machine that has never run a session locally is not misconfigured.
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

// sizeOf returns the total size of a session directory in a human-readable
// form. An unreadable entry contributes nothing rather than failing the
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

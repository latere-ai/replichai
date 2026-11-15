// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/latere-ai/replichai/internal/job"
)

// TestSessionKeyIsStablePerJob checks that the same job always yields the same
// key, including after its state changed, and that another job yields another.
func TestSessionKeyIsStablePerJob(t *testing.T) {
	j := job.Job{ID: "job-1", Phase: job.PhaseResolving}
	first := sessionKey(j)
	j.Phase, j.RepoName = job.PhaseRunning, "open-hai/x"
	if got := sessionKey(j); got != first || first == "" {
		t.Fatalf("key changed for one job: %q then %q", first, got)
	}
	if sessionKey(job.Job{ID: "job-2"}) == first {
		t.Fatal("two jobs share a key")
	}
}

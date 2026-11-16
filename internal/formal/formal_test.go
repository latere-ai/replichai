// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package formal_test renders every state table in ReplicHAI into the Lean and
// TLA+ models under formal/, which the lean and tla gates of lateregate check.
package formal_test

import (
	"flag"
	"testing"

	"latere.ai/x/pkg/statemachine/statemachinetest"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/project"
)

var update = flag.Bool("update", false, "rewrite the formal models under formal/")

// TestFormalModels fails when a table no longer matches the models rendered
// from it. Rerun with -update after changing a table, then build the proofs
// and check the TLA+ models with `go tool lateregate lean tla`.
func TestFormalModels(t *testing.T) {
	statemachinetest.Models(t, "../../formal", *update,
		job.Phases.Graph(),
		access.Grants.Graph(),
		managed.Sessions.Graph(),
		batch.Entries.Graph(),
		project.Publications.Graph(),
	)
}

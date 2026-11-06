// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/wirejson"
)

// EnvironmentFile is the record a local session keeps of what it ran on: the
// harness, the sandbox and the hosts it could reach. The host writes it, never
// the agent, and it is committed beside the work.
const EnvironmentFile = "environment.json"

// RunOf starts a run record from a session's last status: where it ran, on
// which model, when, how it ended and what it used. The CLI and the server
// both build the record here, so the same session is described the same way
// whichever of them finalizes it.
//
// What the status does not carry is left for the caller: where the run sits in
// its project, what model was asked for, and what a local session's own
// outputs add.
func RunOf(status managed.Status) Run {
	run := Run{
		Runtime:            string(status.Ref.Runtime),
		Session:            status.Ref.ID,
		Reason:             string(status.TerminalReason),
		Tokens:             status.Usage.TotalTokens(),
		VendorCostMicroUSD: status.Usage.Details[managed.DetailVendorCost],
	}
	// A runtime whose own platform charged for the session reports that
	// charge as a meter; it is what the session cost.
	if run.VendorCostMicroUSD == 0 {
		run.VendorCostMicroUSD = status.Usage.Extra[managed.MeterChargedCost]
	}
	if status.State != managed.StateUnknown {
		run.State = status.State.String()
	}
	if status.Model.ID != "" {
		run.Model = &RunModel{Provider: status.Model.Provider, ID: status.Model.ID}
	}
	if !status.StartedAt.IsZero() {
		run.StartedAt = status.StartedAt.UTC()
	}
	if !status.FinishedAt.IsZero() {
		run.FinishedAt = status.FinishedAt.UTC()
	}
	return run
}

// WithModels adds every model that served one of the events' calls, when
// more than the one the record already names served: a single-model run's
// record stays as it was.
func (r Run) WithModels(events []managed.Event) Run {
	var served []RunModel
	for _, event := range events {
		if event.Usage == nil || !event.Usage.Model.Valid() {
			continue
		}
		model := RunModel{Provider: event.Usage.Model.Provider, ID: event.Usage.Model.ID}
		if !slices.Contains(served, model) {
			served = append(served, model)
		}
	}
	if len(served) == 0 || (len(served) == 1 && r.Model != nil && *r.Model == served[0]) {
		return r
	}
	r.Models = served
	return r
}

// WithOutputs adds what a local session's own outputs say about the run: the
// harness and the sandbox from its environment record, and how many times the
// work was graded from the grades it kept. outputs is what WorkspaceOutputs
// read, so the record is filled from the session directory and is right even
// for a run that committed nothing.
//
// An environment record that cannot be read is an error, and the record comes
// back with whatever else could be filled.
func (r Run) WithOutputs(outputs map[string][]byte) (Run, error) {
	for name := range outputs {
		if strings.HasPrefix(name, gradeDir+"/verdict-") && strings.HasSuffix(name, ".json") {
			r.Rounds++
		}
	}
	raw, ok := outputs[EnvironmentFile]
	if !ok {
		return r, nil
	}
	// Only the fields the manifest repeats are read. The file stays the full
	// account, with the package versions and the allowlist itself.
	var environment struct {
		Sandbox struct {
			Driver      string `json:"driver"`
			NetworkMode string `json:"network_mode"`
		} `json:"sandbox"`
		Harness struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"harness"`
		Network struct {
			Allowed []string `json:"allowed"`
		} `json:"network"`
	}
	if err := json.Unmarshal(raw, &environment, wirejson.Foreign); err != nil {
		return r, fmt.Errorf("read %s: %w", EnvironmentFile, err)
	}
	r.Harness, r.HarnessVersion = environment.Harness.Name, environment.Harness.Version
	r.Sandbox, r.Network = environment.Sandbox.Driver, environment.Sandbox.NetworkMode
	if allowed := environment.Network.Allowed; len(allowed) > 0 {
		r.NetworkAllowed = len(allowed)
		r.NetworkDigest = allowlistDigest(allowed)
	}
	return r, nil
}

// allowlistDigest identifies a set of hosts as "sha256:<hex>", whatever order
// they were listed in.
func allowlistDigest(hosts []string) string {
	sorted := slices.Clone(hosts)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

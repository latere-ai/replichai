// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

// Outcome is what running one experiment produced. A run that failed to execute
// is recorded with the same standing as one that ran: an analysis that fails on
// conforming data is a finding about the instrument, so a false Ran with the
// captured error is a complete result.
type Outcome struct {
	// Ran is false when the declared command did not execute cleanly. A false
	// Ran with the captured error is a reportable finding, not a failure of the
	// experiment.
	Ran      bool
	Command  string // the resolved command that was executed
	ExitCode int
	Stdout   string
	Stderr   string
	// Recomputed are the headline statistics the analysis emitted, for a
	// released-source comparison against protocol.reported. Empty for a run that
	// produced none, or a source that is not compared.
	Recomputed []Statistic
	// Artifacts are the analysis outputs, checksummed. The bytes live in an
	// ArtifactStore; this records what was produced.
	Artifacts []Artifact
}

// Statistic is one value the analysis recomputed, named to match a
// protocol.reported metric. The value is carried as the analysis emitted it;
// interpreting units and precision is left to the reader of the report.
type Statistic struct {
	Metric string `json:"metric"`
	Value  string `json:"value"`
}

// Artifact is one analysis output, identified by name and content checksum so a
// result stays attributable to exact bytes even though the bytes live elsewhere.
type Artifact struct {
	Name     string `json:"name"`
	Checksum string `json:"checksum"` // "sha256:<hex>"
}

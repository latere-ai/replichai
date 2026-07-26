package experiment

// Outcome is what running one experiment produced. It records a run that
// executed and a run that failed to execute with equal standing: an analysis
// that fails on conforming data is a finding about the instrument, not an error
// to hide, so "it did not run, and here is the error" is a complete result.
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

// Statistic is one value the analysis recomputed, named to line up with a
// protocol.reported metric. The value is carried as the analysis emitted it —
// interpreting units and precision is the report reader's work, not ours.
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

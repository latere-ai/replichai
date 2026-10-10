// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime/debug"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/publish"
)

// runRecord is what this program knows about a run when it finalizes it (046):
// the session's own status, the model that was asked for, the prompt it was
// given, the build that ran it, and, for a session on this machine, what its
// session directory says about the harness, the sandbox and the grading.
//
// A hosted runtime keeps no directory here, so its record carries only what
// the status reports. That is not a failure and is not reported as one.
func runRecord(rt managed.Runtime, status managed.Status, requestedModel, promptDigest string, stderr io.Writer) publish.Run {
	record := publish.RunOf(status)
	record.RequestedModel = requestedModel
	record.PromptDigest = promptDigest
	record.Build = buildRevision()

	located, ok := rt.(workspaceReader)
	if !ok || status.Ref.ID == "" {
		return record
	}
	workspace, err := located.Workspace(status.Ref)
	if err == nil {
		var outputs map[string][]byte
		if outputs, err = publish.WorkspaceOutputs(filepath.Dir(workspace)); err == nil {
			record, err = record.WithOutputs(outputs)
		}
	}
	if err != nil {
		// The run is still finalized. A manifest that says less is better
		// than no manifest, and the gap is named where the operator reads.
		_, _ = fmt.Fprintf(stderr, "  ! the run record is missing what the session directory holds: %v\n", err)
	}
	return record
}

// notStarted is the record of a run that failed before a session existed. The
// repository was already created for it, and without a manifest that
// repository reads as a run nobody can account for.
func notStarted(requestedModel string) publish.Run {
	return publish.Run{
		RequestedModel: requestedModel,
		Build:          buildRevision(),
		State:          managed.StateFailed.String(),
		Reason:         publish.ReasonNotStarted,
	}
}

// buildRevision is the commit this program was built from, with a mark when
// the tree had changes the commit does not hold. It is empty for a build with
// no version control information, such as a test binary.
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified == "true" {
		revision += "+modified"
	}
	return revision
}

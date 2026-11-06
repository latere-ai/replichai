// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package experiment runs a reproduction's declared analysis on new data.
//
// [007a] leaves an instrument, instrument.json plus src/ in a published
// reproduction, that nothing executes. This package is the executor and the
// domain model around it: an experiment binds one instrument to one data
// source and records what running it produced.
//
// The 007 track depends on a package-import rule: experiment imports
// instrument, provider, and publish, and internal/audit never imports
// experiment. An auditor that could read an experiment could let a cohort
// inform a verdict; the missing import edge forbids it.
package experiment

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/publish"
	"github.com/latere-ai/replichai/internal/wirejson"
)

// Source is where an experiment's input data comes from. Four are named so the
// domain model is complete; 007b implements the two offline ones, and
// synthetic (007c) and human (007d) follow.
type Source string

// The data sources an experiment can bind to.
const (
	// SourceReleased re-runs the paper's own released data: a re-analysis, whose
	// result is compared against protocol.reported.
	SourceReleased Source = "released"
	// SourceUploaded runs a dataset the researcher brings: their finding on the
	// paper's instrument, never published because it is their data.
	SourceUploaded Source = "uploaded"
	// SourceSynthetic drives an LLM cohort. The result is a pilot and is
	// evidence about nobody (007c).
	SourceSynthetic Source = "synthetic"
	// SourceHuman collects from participants via a hosted page (007d).
	SourceHuman Source = "human"
)

// Offline reports whether a source needs no participants, consent, or public
// write path. These are the two sources 007b runs.
func (s Source) Offline() bool { return s == SourceReleased || s == SourceUploaded }

// InstrumentPath is where the instrument declaration lives in a reproduction.
const InstrumentPath = "instrument.json"

// Errors returned by resolution.
var (
	// ErrNoInstrument reports that a repository has no instrument.json at the
	// resolved commit, so there is nothing to experiment on.
	ErrNoInstrument = errors.New("no instrument.json in the reproduction")
)

// InstrumentRef pins an instrument to an exact commit of its reproduction. A
// result stays attributable to this ref even after the reproduction gains
// commits, which is why the commit is recorded rather than re-read.
type InstrumentRef struct {
	Repo   publish.Repo
	Commit string
}

// Instrument is a resolved instrument: the pinned reference plus the declaration
// read there. It is never stored: the store keeps only the InstrumentRef and
// the declaration is re-resolved from the repository, so nothing drifts from
// the manifests, which are the source of truth under 003's rule.
type Instrument struct {
	Ref  InstrumentRef
	Decl instrument.Instrument
}

// Forge is the read side of a reproduction's host that resolution needs: pin a
// repository to its current head, and read a file at a pinned commit. It is a
// subset of what internal/github offers, expressed as an interface so
// resolution tests against a fake with no network.
type Forge interface {
	// Head returns the repository's current head commit.
	Head(ctx context.Context, repo publish.Repo) (commit string, err error)
	// ReadFileAt reads one file at a pinned commit. ok is false when the file is
	// absent there.
	ReadFileAt(ctx context.Context, repo publish.Repo, commit, path string) (content []byte, ok bool, err error)
}

// Resolve pins repo to its current head commit and reads the instrument there.
// An experiment records the exact commit it ran, so re-resolving the same
// InstrumentRef later returns the same declaration regardless of the commits
// the reproduction has since gained.
func Resolve(ctx context.Context, f Forge, repo publish.Repo) (Instrument, error) {
	commit, err := f.Head(ctx, repo)
	if err != nil {
		return Instrument{}, fmt.Errorf("pin %s/%s to head: %w", repo.Owner, repo.Name, err)
	}
	return ResolveAt(ctx, f, InstrumentRef{Repo: repo, Commit: commit})
}

// ResolveAt reads the instrument at an already-pinned reference, so a stored
// experiment re-resolves the exact instrument it ran.
func ResolveAt(ctx context.Context, f Forge, ref InstrumentRef) (Instrument, error) {
	raw, ok, err := f.ReadFileAt(ctx, ref.Repo, ref.Commit, InstrumentPath)
	if err != nil {
		return Instrument{}, fmt.Errorf("read %s at %s: %w", InstrumentPath, short(ref.Commit), err)
	}
	if !ok {
		return Instrument{}, fmt.Errorf("%w: %s/%s@%s", ErrNoInstrument, ref.Repo.Owner, ref.Repo.Name, short(ref.Commit))
	}
	var decl instrument.Instrument
	if err := json.Unmarshal(raw, &decl, wirejson.Foreign); err != nil {
		return Instrument{}, fmt.Errorf("parse %s at %s: %w", InstrumentPath, short(ref.Commit), err)
	}
	return Instrument{Ref: ref, Decl: decl}, nil
}

// short abbreviates a commit sha for messages, tolerating a short or empty one.
func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	if commit == "" {
		return "?"
	}
	return commit
}

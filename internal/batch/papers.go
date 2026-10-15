// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"latere.ai/x/pkg/atomicfile"

	"github.com/latere-ai/replichai/internal/paper"
)

// PapersFile records the inputs a study handed its runs (046): the copy of each
// paper and the snapshot of each artifact the project lists for it. It sits
// beside batch.json, and the bytes themselves under papers/, so a resumed study
// hands its remaining runs the bytes its first runs read rather than ones
// fetched again, which may differ.
const PapersFile = "papers.json"

// papersDir is where a study keeps its copies and snapshots, named by digest.
const papersDir = "papers"

// papersVersion is the shape of PapersFile.
const papersVersion = 1

// ErrMissingInputs reports a study some of whose papers or artifacts could not
// be given to its runs. Nothing runs: runs that each found their own would
// compare their results against whichever version each happened to read.
var ErrMissingInputs = errors.New("every run of a paper is handed the same inputs, and some could not be had")

// PaperCopy is the record of the inputs one paper's runs are handed.
type PaperCopy struct {
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	// Source is where the copy came from: the address it was fetched from,
	// or the file the project named.
	Source string `json:"source"`
	// File is where the study keeps the copy, relative to its directory.
	File string `json:"file"`
	// Artifacts are the snapshots of what the project lists the paper's
	// authors as having released, in the order the project lists them.
	Artifacts []ArtifactCopy `json:"artifacts,omitempty"`
}

// ArtifactCopy is the record of one artifact snapshot.
type ArtifactCopy struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Commit string `json:"commit,omitempty"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}

type papersRecord struct {
	Version int                  `json:"version"`
	Papers  map[string]PaperCopy `json:"papers"`
}

// FetchPaper obtains a copy of a paper the study has none of.
type FetchPaper func(ctx context.Context, reference string) (paper.Copy, error)

// FetchArtifact takes a snapshot of one artifact a project lists.
type FetchArtifact func(ctx context.Context, source string) (paper.Artifact, error)

// Sources is where a study's inputs come from when it holds none yet.
type Sources struct {
	Paper    FetchPaper
	Artifact FetchArtifact
}

// StudyInputs is what every run of one paper is handed: one copy of the paper,
// and one snapshot of each artifact the project lists for it.
type StudyInputs struct {
	Paper     paper.Copy
	Artifacts []paper.Artifact
}

// Inputs gives every paper of a study its inputs, before any run starts, so
// every run of a paper reads the same text and reproduces from the same
// released materials (046). The copy of a paper is the one a resumed study
// already holds, else the file the project names for it, else one fetched now;
// each artifact the project lists is snapshotted once in the same way. Each is
// kept under root and recorded in PapersFile.
//
// A paper or an artifact that cannot be had fails the whole study before
// anything runs, and the error names every one and what to do about it.
//
// A resumed study refuses inputs that are not what its first runs were handed:
// a kept copy or snapshot that no longer matches its record, a file the
// project names that has changed, or a list of artifacts that is not the one
// the study started with.
func Inputs(ctx context.Context, root string, entries []Resolved, sources Sources, resume bool) (map[string]StudyInputs, error) {
	listed, order, err := listedInputs(entries)
	if err != nil {
		return nil, err
	}
	record := papersRecord{Version: papersVersion, Papers: map[string]PaperCopy{}}
	if resume {
		if record, err = loadPapers(root); err != nil {
			return nil, err
		}
	}

	inputs := make(map[string]StudyInputs, len(order))
	var missing []string
	for _, reference := range order {
		wanted := listed[reference]
		if kept, ok := record.Papers[reference]; ok {
			held, err := keptInputs(root, reference, kept, wanted)
			if err != nil {
				return nil, err
			}
			inputs[reference] = held
			continue
		}
		var (
			held  StudyInputs
			entry PaperCopy
			short bool
		)
		if wanted.file != "" {
			if held.Paper, err = readSupplied(root, wanted.file); err != nil {
				return nil, fmt.Errorf("%s: %w", reference, err)
			}
		} else if held.Paper, err = sources.Paper(ctx, reference); err != nil {
			missing = append(missing, fmt.Sprintf("  %s: %v\n    name a copy under the paper, as file: path/to/paper.pdf", reference, err))
			short = true
		}
		for _, source := range wanted.artifacts {
			snapshot, err := sources.Artifact(ctx, source)
			if err != nil {
				missing = append(missing, fmt.Sprintf("  %s: the artifact %s: %v\n    name its direct download address, or leave it out", reference, source, err))
				short = true
				continue
			}
			held.Artifacts = append(held.Artifacts, snapshot)
		}
		if short {
			continue
		}
		if entry, err = keepPaper(root, held.Paper); err != nil {
			return nil, err
		}
		for _, snapshot := range held.Artifacts {
			kept, err := keepArtifact(root, snapshot)
			if err != nil {
				return nil, err
			}
			entry.Artifacts = append(entry.Artifacts, kept)
		}
		record.Papers[reference] = entry
		inputs[reference] = held
	}
	if err := savePapers(root, record); err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w:\n%s", ErrMissingInputs, strings.Join(missing, "\n"))
	}
	return inputs, nil
}

// listing is what the project names for one paper: a file to use as its copy,
// and the artifacts to snapshot.
type listing struct {
	file      string
	artifacts []string
}

// listedInputs is what the project names for each paper, and the papers in the
// order they first appear. The runs of one paper share their inputs, so two
// entries for one paper that name different files, or different artifacts,
// are refused; an entry that names none takes the other's.
func listedInputs(entries []Resolved) (map[string]listing, []string, error) {
	listed := map[string]listing{}
	var order []string
	for _, entry := range entries {
		reference := entry.Reference()
		file := strings.TrimSpace(entry.File)
		var artifacts []string
		for _, source := range entry.Artifacts {
			if source = strings.TrimSpace(source); source != "" {
				artifacts = append(artifacts, source)
			}
		}
		previous, seen := listed[reference]
		if !seen {
			order = append(order, reference)
			listed[reference] = listing{file: file, artifacts: artifacts}
			continue
		}
		switch {
		case file == "" || file == previous.file:
		case previous.file == "":
			previous.file = file
		default:
			return nil, nil, fmt.Errorf("%s names two files, %s and %s; every run of a paper reads one copy", reference, previous.file, file)
		}
		switch {
		case len(artifacts) == 0 || slices.Equal(artifacts, previous.artifacts):
		case len(previous.artifacts) == 0:
			previous.artifacts = artifacts
		default:
			return nil, nil, fmt.Errorf("%s lists its artifacts twice, differently; every run of a paper is handed the same ones", reference)
		}
		listed[reference] = previous
	}
	return listed, order, nil
}

// keptInputs reads the inputs a resumed study already holds for one paper and
// checks they are what the project still names.
func keptInputs(root, reference string, kept PaperCopy, wanted listing) (StudyInputs, error) {
	copied, err := keptCopy(root, kept)
	if err != nil {
		return StudyInputs{}, fmt.Errorf("%s: %w", reference, err)
	}
	if wanted.file != "" {
		supplied, err := readSupplied(root, wanted.file)
		if err != nil {
			return StudyInputs{}, fmt.Errorf("%s: %w", reference, err)
		}
		if supplied.SHA256 != copied.SHA256 {
			return StudyInputs{}, fmt.Errorf("%s: %s has changed since the study started; its runs were handed %s. "+
				"Restore the file, or start the study again without --resume", reference, wanted.file, kept.File)
		}
	}
	var recorded []string
	for _, artifact := range kept.Artifacts {
		recorded = append(recorded, artifact.Source)
	}
	if !slices.Equal(recorded, wanted.artifacts) {
		return StudyInputs{}, fmt.Errorf("%s: the artifacts the project lists are not the ones the study started with; "+
			"restore the list, or start the study again without --resume", reference)
	}
	held := StudyInputs{Paper: copied}
	for _, artifact := range kept.Artifacts {
		snapshot, err := keptSnapshot(root, artifact)
		if err != nil {
			return StudyInputs{}, fmt.Errorf("%s: the artifact %s: %w", reference, artifact.Source, err)
		}
		held.Artifacts = append(held.Artifacts, snapshot)
	}
	return held, nil
}

// readSupplied reads a file the project names, relative to the project file.
func readSupplied(root, file string) (paper.Copy, error) {
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	//nolint:gosec // the path is one the operator's project file names
	body, err := os.ReadFile(path)
	if err != nil {
		return paper.Copy{}, fmt.Errorf("read the copy the project names: %w", err)
	}
	if len(body) == 0 {
		return paper.Copy{}, fmt.Errorf("the copy the project names, %s, is empty", file)
	}
	return paper.CopyOf(body, file), nil
}

// keepPaper stores a paper's copy under the study's directory and returns its
// record.
func keepPaper(root string, copied paper.Copy) (PaperCopy, error) {
	extension := ".bin"
	if copied.MediaType == "application/pdf" {
		extension = ".pdf"
	}
	relative, err := keepBytes(root, copied.SHA256, extension, copied.Content)
	if err != nil {
		return PaperCopy{}, err
	}
	return PaperCopy{SHA256: copied.SHA256, MediaType: copied.MediaType, Source: copied.Source, File: relative}, nil
}

// keepArtifact stores an artifact snapshot under the study's directory and
// returns its record.
func keepArtifact(root string, snapshot paper.Artifact) (ArtifactCopy, error) {
	extension := filepath.Ext(snapshot.Name)
	if extension == "" {
		extension = ".bin"
	}
	relative, err := keepBytes(root, snapshot.SHA256, extension, snapshot.Content)
	if err != nil {
		return ArtifactCopy{}, err
	}
	return ArtifactCopy{
		Source: snapshot.Source, Kind: snapshot.Kind, Commit: snapshot.Commit, Name: snapshot.Name,
		SHA256: snapshot.SHA256, File: relative,
	}, nil
}

// keepBytes stores content under the study's directory, named by its digest,
// and returns where, relative to the study. A file already there under that
// name is the same bytes and is not written again.
func keepBytes(root, sum, extension string, content []byte) (string, error) {
	relative := filepath.ToSlash(filepath.Join(papersDir, "sha256-"+sum+extension))
	target := filepath.Join(root, filepath.FromSlash(relative))
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", papersDir, err)
		}
		if err := atomicfile.Write(target, content, 0o444); err != nil {
			return "", fmt.Errorf("keep the study's input: %w", err)
		}
	} else if err != nil {
		return "", err
	}
	return relative, nil
}

// keptCopy reads a copy the study already holds and checks it is the one its
// record names.
func keptCopy(root string, kept PaperCopy) (paper.Copy, error) {
	body, err := keptBytes(root, kept.File, kept.SHA256, "the study's copy of the paper")
	if err != nil {
		return paper.Copy{}, err
	}
	return paper.CopyOf(body, kept.Source), nil
}

// keptSnapshot reads an artifact snapshot the study already holds and checks it
// is the one its record names.
func keptSnapshot(root string, kept ArtifactCopy) (paper.Artifact, error) {
	body, err := keptBytes(root, kept.File, kept.SHA256, "the study's snapshot")
	if err != nil {
		return paper.Artifact{}, err
	}
	return paper.SnapshotOf(kept.Source, kept.Kind, kept.Commit, kept.Name, body), nil
}

// keptBytes reads a file the study kept and checks its digest against the
// record.
func keptBytes(root, file, sum, what string) ([]byte, error) {
	//nolint:gosec // a path under the study's own directory, from its own record
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, fmt.Errorf("%s is gone: %w", what, err)
	}
	if paper.CopyOf(body, "").SHA256 != sum {
		return nil, fmt.Errorf("%s, %s, no longer has the digest its runs were handed", what, file)
	}
	return body, nil
}

func loadPapers(root string) (papersRecord, error) {
	record := papersRecord{Version: papersVersion, Papers: map[string]PaperCopy{}}
	//nolint:gosec // the study's own record, beside its batch.json
	body, err := os.ReadFile(filepath.Join(root, PapersFile))
	if errors.Is(err, fs.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return record, fmt.Errorf("read %s: %w", PapersFile, err)
	}
	if record.Papers == nil {
		record.Papers = map[string]PaperCopy{}
	}
	return record, nil
}

func savePapers(root string, record papersRecord) error {
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(root, PapersFile), append(body, '\n'), 0o644)
}

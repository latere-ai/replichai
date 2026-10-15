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
	"strings"

	"latere.ai/x/pkg/atomicfile"

	"github.com/latere-ai/replichai/internal/paper"
)

// PapersFile records the copy of each paper a study handed its runs (046). It
// sits beside batch.json, and the copies themselves under papers/, so a resumed
// study hands its remaining runs the bytes its first runs read rather than a
// copy fetched again, which may differ.
const PapersFile = "papers.json"

// papersDir is where a study keeps its copies, named by digest.
const papersDir = "papers"

// papersVersion is the shape of PapersFile.
const papersVersion = 1

// PaperCopy is the record of the copy one paper's runs are handed.
type PaperCopy struct {
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	// Source is where the copy came from: the address it was fetched from,
	// or the file the project named.
	Source string `json:"source"`
	// File is where the study keeps the copy, relative to its directory.
	File string `json:"file"`
}

type papersRecord struct {
	Version int                  `json:"version"`
	Papers  map[string]PaperCopy `json:"papers"`
}

// FetchPaper obtains a copy of a paper the study has none of.
type FetchPaper func(ctx context.Context, reference string) (paper.Copy, error)

// Papers gives every paper of a study one copy, before any run starts, so
// every run of a paper reads the same text (046). The copy of a paper is the
// one a resumed study already holds, else the file the project names for it,
// else one fetched now. Each is kept under root and recorded in PapersFile.
//
// A paper with no copy fails the whole study before anything runs. A study
// whose runs found their own copies would compare its results against
// whichever version each run happened to read, which is the spread this exists
// to remove, so the error names every such paper and how to supply a file.
//
// A resumed study refuses a copy that no longer matches its record, and a file
// the project names that has changed since: either would hand the remaining
// runs other bytes than the first runs read.
func Papers(ctx context.Context, root string, entries []Resolved, fetch FetchPaper, resume bool) (map[string]paper.Copy, error) {
	named, order, err := namedFiles(entries)
	if err != nil {
		return nil, err
	}
	record := papersRecord{Version: papersVersion, Papers: map[string]PaperCopy{}}
	if resume {
		if record, err = loadPapers(root); err != nil {
			return nil, err
		}
	}

	copies := make(map[string]paper.Copy, len(order))
	var missing []string
	for _, reference := range order {
		if kept, ok := record.Papers[reference]; ok {
			copied, err := keptCopy(root, kept)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", reference, err)
			}
			if file := named[reference]; file != "" {
				supplied, err := readSupplied(root, file)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", reference, err)
				}
				if supplied.SHA256 != copied.SHA256 {
					return nil, fmt.Errorf("%s: %s has changed since the study started; its runs were handed %s. "+
						"Restore the file, or start the study again without --resume", reference, file, kept.File)
				}
			}
			copies[reference] = copied
			continue
		}
		var copied paper.Copy
		if file := named[reference]; file != "" {
			copied, err = readSupplied(root, file)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", reference, err)
			}
		} else if copied, err = fetch(ctx, reference); err != nil {
			missing = append(missing, fmt.Sprintf("  %s: %v", reference, err))
			continue
		}
		kept, err := keep(root, copied)
		if err != nil {
			return nil, err
		}
		record.Papers[reference] = kept
		copies[reference] = copied
	}
	if err := savePapers(root, record); err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: every run of a paper reads the same copy, and %d paper(s) have none:\n%s\n"+
			"Name a copy for each in the project file, as file: path/to/paper.pdf under the paper, and run again",
			paper.ErrNoFullText, len(missing), strings.Join(missing, "\n"))
	}
	return copies, nil
}

// namedFiles is the file the project names for each paper, and the papers in
// the order they first appear. The runs of one paper share one copy, so two
// entries for one paper that name different files are refused.
func namedFiles(entries []Resolved) (map[string]string, []string, error) {
	named := map[string]string{}
	var order []string
	for _, entry := range entries {
		reference := entry.Reference()
		file := strings.TrimSpace(entry.File)
		previous, seen := named[reference]
		if !seen {
			order = append(order, reference)
			named[reference] = file
			continue
		}
		switch {
		case file == "" || file == previous:
		case previous == "":
			named[reference] = file
		default:
			return nil, nil, fmt.Errorf("%s names two files, %s and %s; every run of a paper reads one copy", reference, previous, file)
		}
	}
	return named, order, nil
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

// keep stores a copy under the study's directory, named by its digest, and
// returns its record. A copy already there under that name is the same bytes
// and is not written again.
func keep(root string, copied paper.Copy) (PaperCopy, error) {
	extension := ".bin"
	if copied.MediaType == "application/pdf" {
		extension = ".pdf"
	}
	relative := filepath.ToSlash(filepath.Join(papersDir, "sha256-"+copied.SHA256+extension))
	target := filepath.Join(root, filepath.FromSlash(relative))
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return PaperCopy{}, fmt.Errorf("create %s: %w", papersDir, err)
		}
		if err := atomicfile.Write(target, copied.Content, 0o444); err != nil {
			return PaperCopy{}, fmt.Errorf("keep the copy of the paper: %w", err)
		}
	} else if err != nil {
		return PaperCopy{}, err
	}
	return PaperCopy{SHA256: copied.SHA256, MediaType: copied.MediaType, Source: copied.Source, File: relative}, nil
}

// keptCopy reads a copy the study already holds and checks it is the one its
// record names.
func keptCopy(root string, kept PaperCopy) (paper.Copy, error) {
	//nolint:gosec // a path under the study's own directory, from its own record
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(kept.File)))
	if err != nil {
		return paper.Copy{}, fmt.Errorf("the study's copy of the paper is gone: %w", err)
	}
	copied := paper.CopyOf(body, kept.Source)
	if copied.SHA256 != kept.SHA256 {
		return paper.Copy{}, fmt.Errorf("the study's copy of the paper, %s, no longer has the digest its runs were handed", kept.File)
	}
	return copied, nil
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

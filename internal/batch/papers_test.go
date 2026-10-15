// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/paper"
)

// fetcher counts how often each paper is fetched, and fetches only the papers
// it holds.
type fetcher struct {
	held  map[string]string
	calls map[string]int
}

// sources is the fetcher as a study's sources: papers from held, and
// artifacts from held too, under their address.
func (f *fetcher) sources() Sources { return Sources{Paper: f.fetch, Artifact: f.snapshot} }

func (f *fetcher) snapshot(_ context.Context, source string) (paper.Artifact, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[source]++
	body, ok := f.held[source]
	if !ok {
		return paper.Artifact{}, errors.New("404 Not Found")
	}
	return paper.SnapshotOf(source, paper.ArtifactGit, "c0ffee", "system.bundle", []byte(body)), nil
}

func (f *fetcher) fetch(_ context.Context, reference string) (paper.Copy, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[reference]++
	body, ok := f.held[reference]
	if !ok {
		return paper.Copy{}, errors.New("403 Forbidden")
	}
	return paper.CopyOf([]byte(body), "https://example.org/"+reference), nil
}

func resolved(t *testing.T, file File) []Resolved {
	t.Helper()
	entries, err := file.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	return entries
}

// A paper is fetched once for the study, however many runs it has, and the
// copy is kept beside the study under its digest.
func TestEachPaperIsFetchedOncePerStudy(t *testing.T) {
	root := t.TempDir()
	source := &fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7 a", "10.1145/3654777.3676417": "%PDF-1.7 b"}}
	entries := resolved(t, File{Runs: 3, Papers: []Entry{{Paper: "arXiv:2605.19151"}, {Paper: "10.1145/3654777.3676417"}}})
	copies, err := Inputs(context.Background(), root, entries, source.sources(), false)
	if err != nil {
		t.Fatalf("Papers: %v", err)
	}
	if len(copies) != 2 || source.calls["arXiv:2605.19151"] != 1 || source.calls["10.1145/3654777.3676417"] != 1 {
		t.Fatalf("copies = %d, fetches = %v; want each paper fetched once", len(copies), source.calls)
	}
	kept := copies["arXiv:2605.19151"].Paper
	body, err := os.ReadFile(filepath.Join(root, "papers", "sha256-"+kept.SHA256+".pdf"))
	if err != nil || string(body) != "%PDF-1.7 a" {
		t.Fatalf("the copy was not kept under its digest: %q, %v", body, err)
	}
	var record papersRecord
	raw, err := os.ReadFile(filepath.Join(root, PapersFile))
	if err != nil || json.Unmarshal(raw, &record) != nil {
		t.Fatalf("read %s: %v", PapersFile, err)
	}
	if got := record.Papers["arXiv:2605.19151"]; got.SHA256 != kept.SHA256 || got.Source != "https://example.org/arXiv:2605.19151" {
		t.Fatalf("record = %+v", got)
	}
}

// A file the project names is the copy, read relative to the project file, and
// nothing is fetched for that paper.
func TestAFileTheProjectNamesIsTheCopy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "kay.pdf"), []byte("%PDF-1.7 kay"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &fetcher{}
	entries := resolved(t, File{Papers: []Entry{
		{Paper: "10.1145/2702123.2702520", Runs: 2},
		{Paper: "10.1145/2702123.2702520", File: "kay.pdf"},
	}})
	copies, err := Inputs(context.Background(), root, entries, source.sources(), false)
	if err != nil {
		t.Fatalf("Papers: %v", err)
	}
	if string(copies["10.1145/2702123.2702520"].Paper.Content) != "%PDF-1.7 kay" || len(source.calls) != 0 {
		t.Fatalf("copies = %+v, fetches = %v", copies, source.calls)
	}
}

// A paper with no copy fails the study before anything runs, and the error
// names every such paper and how to supply one.
func TestAStudyWithAPaperItCannotFetchDoesNotStart(t *testing.T) {
	source := &fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7"}}
	entries := resolved(t, File{Papers: []Entry{
		{Paper: "arXiv:2605.19151"}, {Paper: "10.1145/2702123.2702520"}, {Paper: "10.1145/2858036.2858122"},
	}})
	_, err := Inputs(context.Background(), t.TempDir(), entries, source.sources(), false)
	if !errors.Is(err, ErrMissingInputs) {
		t.Fatalf("err = %v, want ErrMissingInputs", err)
	}
	for _, want := range []string{"10.1145/2702123.2702520", "10.1145/2858036.2858122", "file: path/to/paper.pdf"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to say %q", err, want)
		}
	}
}

// A resumed study hands its remaining runs the copy its first runs read, and
// fetches nothing again.
func TestAResumedStudyKeepsItsCopies(t *testing.T) {
	root := t.TempDir()
	entries := resolved(t, File{Papers: []Entry{{Paper: "arXiv:2605.19151"}}})
	first, err := Inputs(context.Background(), root, entries, (&fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7 v1"}}).sources(), false)
	if err != nil {
		t.Fatalf("Papers: %v", err)
	}
	// The paper has since changed where it is fetched from.
	later := &fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7 v2"}}
	resumed, err := Inputs(context.Background(), root, entries, later.sources(), true)
	if err != nil {
		t.Fatalf("Papers on resume: %v", err)
	}
	if resumed["arXiv:2605.19151"].Paper.SHA256 != first["arXiv:2605.19151"].Paper.SHA256 || len(later.calls) != 0 {
		t.Fatalf("the resumed study fetched again or changed its copy: %v", later.calls)
	}
	// A paper added to the project since is fetched and kept.
	grown := resolved(t, File{Papers: []Entry{{Paper: "arXiv:2605.19151"}, {Paper: "arXiv:2605.00001"}}})
	later.held["arXiv:2605.00001"] = "%PDF-1.7 new"
	if copies, err := Inputs(context.Background(), root, grown, later.sources(), true); err != nil || len(copies) != 2 {
		t.Fatalf("a paper added on resume: %v, %v", copies, err)
	}
}

// A resumed study refuses a copy that is not what its first runs read.
func TestAResumedStudyRefusesACopyThatChanged(t *testing.T) {
	root := t.TempDir()
	supplied := filepath.Join(root, "kay.pdf")
	if err := os.WriteFile(supplied, []byte("%PDF-1.7 kay"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := resolved(t, File{Papers: []Entry{{Paper: "10.1145/2702123.2702520", File: "kay.pdf"}}})
	copies, err := Inputs(context.Background(), root, entries, (&fetcher{}).sources(), false)
	if err != nil {
		t.Fatalf("Papers: %v", err)
	}

	if err := os.WriteFile(supplied, []byte("%PDF-1.7 kay, revised"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inputs(context.Background(), root, entries, (&fetcher{}).sources(), true); err == nil || !strings.Contains(err.Error(), "has changed") {
		t.Fatalf("a changed file on resume = %v", err)
	}
	if err := os.Remove(supplied); err != nil {
		t.Fatal(err)
	}
	if _, err := Inputs(context.Background(), root, entries, (&fetcher{}).sources(), true); err == nil {
		t.Fatal("a named file that is gone was accepted on resume")
	}

	kept := filepath.Join(root, "papers", "sha256-"+copies["10.1145/2702123.2702520"].Paper.SHA256+".pdf")
	if err := os.Chmod(kept, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("%PDF-1.7 tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	unnamed := resolved(t, File{Papers: []Entry{{Paper: "10.1145/2702123.2702520"}}})
	if _, err := Inputs(context.Background(), root, unnamed, (&fetcher{}).sources(), true); err == nil || !strings.Contains(err.Error(), "no longer has the digest") {
		t.Fatalf("a tampered copy on resume = %v", err)
	}
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}
	if _, err := Inputs(context.Background(), root, unnamed, (&fetcher{}).sources(), true); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("a missing copy on resume = %v", err)
	}
}

func TestTheCopiesOfAStudyAreRefusedWhenTheyCannotBeTold(t *testing.T) {
	root := t.TempDir()
	twoFiles := resolved(t, File{Papers: []Entry{
		{Paper: "10.1145/2702123.2702520", File: "a.pdf"}, {Paper: "10.1145/2702123.2702520", File: "b.pdf"},
	}})
	if _, err := Inputs(context.Background(), root, twoFiles, (&fetcher{}).sources(), false); err == nil || !strings.Contains(err.Error(), "two files") {
		t.Fatalf("two files for one paper = %v", err)
	}
	empty := filepath.Join(root, "empty.pdf")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{empty, "missing.pdf"} {
		entries := resolved(t, File{Papers: []Entry{{Paper: "10.1145/2702123.2702520", File: file}}})
		if _, err := Inputs(context.Background(), root, entries, (&fetcher{}).sources(), false); err == nil {
			t.Errorf("the file %s was accepted as a copy", file)
		}
	}
	if err := os.WriteFile(filepath.Join(root, PapersFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := resolved(t, File{Papers: []Entry{{Paper: "arXiv:2605.19151"}}})
	if _, err := Inputs(context.Background(), root, entries, (&fetcher{}).sources(), true); err == nil {
		t.Fatal("an unreadable record was accepted on resume")
	}
}

// The network profile is a default of the file that a paper may override, as
// the model and the agent system are.
func TestTheNetworkProfileIsAFileDefaultAPaperOverrides(t *testing.T) {
	entries := resolved(t, File{Network: "artifacts", Papers: []Entry{{Paper: "a"}, {Paper: "b", Network: "open"}}})
	if entries[0].Network != "artifacts" || entries[1].Network != "open" {
		t.Fatalf("networks = %q, %q", entries[0].Network, entries[1].Network)
	}
}

// Each artifact a paper lists is snapshotted once for the study, however many
// runs the paper has, and recorded with its commit beside the paper's copy.
func TestEachListedArtifactIsSnapshottedOncePerStudy(t *testing.T) {
	root := t.TempDir()
	const repo = "https://github.com/authors/system"
	source := &fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7", repo: "bundle bytes"}}
	entries := resolved(t, File{Papers: []Entry{
		{Paper: "arXiv:2605.19151", Runs: 2, Artifacts: []string{repo}},
		{Paper: "arXiv:2605.19151"},
	}})
	inputs, err := Inputs(context.Background(), root, entries, source.sources(), false)
	if err != nil {
		t.Fatalf("Inputs: %v", err)
	}
	held := inputs["arXiv:2605.19151"].Artifacts
	if len(held) != 1 || string(held[0].Content) != "bundle bytes" || source.calls[repo] != 1 {
		t.Fatalf("artifacts = %+v, snapshots taken = %v", held, source.calls)
	}
	var record papersRecord
	raw, err := os.ReadFile(filepath.Join(root, PapersFile))
	if err != nil || json.Unmarshal(raw, &record) != nil {
		t.Fatalf("read %s: %v", PapersFile, err)
	}
	kept := record.Papers["arXiv:2605.19151"].Artifacts
	if len(kept) != 1 || kept[0].Source != repo || kept[0].Commit != "c0ffee" || kept[0].Kind != paper.ArtifactGit ||
		kept[0].File != "papers/sha256-"+held[0].SHA256+".bundle" {
		t.Fatalf("record = %+v", kept)
	}

	// A resumed study hands its runs the same snapshot and takes no other.
	later := &fetcher{}
	resumed, err := Inputs(context.Background(), root, entries, later.sources(), true)
	if err != nil || resumed["arXiv:2605.19151"].Artifacts[0].SHA256 != held[0].SHA256 || len(later.calls) != 0 {
		t.Fatalf("resumed = %+v, %v; snapshots taken = %v", resumed, err, later.calls)
	}

	// A list changed since the study started is refused on resume.
	changed := resolved(t, File{Papers: []Entry{{Paper: "arXiv:2605.19151", Artifacts: []string{repo, "https://osf.io/abcd/download"}}}})
	if _, err := Inputs(context.Background(), root, changed, later.sources(), true); err == nil || !strings.Contains(err.Error(), "not the ones the study started with") {
		t.Fatalf("a changed list on resume = %v", err)
	}

	// So is a snapshot whose bytes changed.
	snapshot := filepath.Join(root, filepath.FromSlash(kept[0].File))
	if err := os.Chmod(snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte("other bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inputs(context.Background(), root, entries, later.sources(), true); err == nil || !strings.Contains(err.Error(), "no longer has the digest") {
		t.Fatalf("a changed snapshot on resume = %v", err)
	}
}

// An artifact that cannot be snapshotted stops the study before anything runs,
// naming the paper and the artifact, and the paper is not half recorded.
func TestAnArtifactThatCannotBeSnapshottedStopsTheStudy(t *testing.T) {
	root := t.TempDir()
	source := &fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7"}}
	entries := resolved(t, File{Papers: []Entry{{Paper: "arXiv:2605.19151", Artifacts: []string{"https://osf.io/abcd", " "}}}})
	_, err := Inputs(context.Background(), root, entries, source.sources(), false)
	if !errors.Is(err, ErrMissingInputs) || !strings.Contains(err.Error(), "https://osf.io/abcd") || !strings.Contains(err.Error(), "direct download address") {
		t.Fatalf("err = %v", err)
	}
	record, err := loadPapers(root)
	if err != nil || len(record.Papers) != 0 {
		t.Fatalf("a paper whose inputs are incomplete was recorded: %+v, %v", record, err)
	}
}

// Two entries of one paper must list the same artifacts, or one of them none.
func TestTwoListsOfArtifactsForOnePaperAreRefused(t *testing.T) {
	entries := resolved(t, File{Papers: []Entry{
		{Paper: "arXiv:2605.19151", Artifacts: []string{"https://github.com/a/b"}},
		{Paper: "arXiv:2605.19151", Artifacts: []string{"https://github.com/a/c"}},
	}})
	if _, err := Inputs(context.Background(), t.TempDir(), entries, (&fetcher{}).sources(), false); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("two lists = %v", err)
	}
	later := resolved(t, File{Papers: []Entry{
		{Paper: "arXiv:2605.19151"},
		{Paper: "arXiv:2605.19151", Artifacts: []string{"https://github.com/a/b"}},
	}})
	source := &fetcher{held: map[string]string{"arXiv:2605.19151": "%PDF-1.7", "https://github.com/a/b": "bundle"}}
	inputs, err := Inputs(context.Background(), t.TempDir(), later, source.sources(), false)
	if err != nil || len(inputs["arXiv:2605.19151"].Artifacts) != 1 {
		t.Fatalf("a list given by the second entry = %+v, %v", inputs, err)
	}
}

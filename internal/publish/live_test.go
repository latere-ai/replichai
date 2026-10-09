// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/verdict"
)

// fakeLiveForge is an in-memory repository standing in for the github client, so
// the live finalize is tested with no network.
type fakeLiveForge struct {
	head  string
	tree  []string
	files map[string][]byte
	puts  int      // PutFile calls
	msgs  []string // CommitFiles messages, in order
}

func newFakeForge(tree []string) *fakeLiveForge {
	return &fakeLiveForge{head: "deadbeef", tree: tree, files: map[string][]byte{}}
}

func (f *fakeLiveForge) HeadCommit(context.Context, string, string) (string, error) {
	return f.head, nil
}

// A real forge cannot list the tree of a commit that does not exist, and a
// fake that answers one anyway hides the case this whole path is about.
func (f *fakeLiveForge) TreePaths(_ context.Context, _, _, commit string) ([]string, error) {
	if commit == "" {
		return nil, errors.New("no commit to read a tree at")
	}
	return f.tree, nil
}

func (f *fakeLiveForge) GetFileContent(_ context.Context, _, _, path string) ([]byte, bool, error) {
	b, ok := f.files[path]
	return b, ok, nil
}

func (f *fakeLiveForge) PutFile(_ context.Context, _, _, path, _ string, content []byte) error {
	f.files[path] = content
	f.track(path)
	f.puts++
	return nil
}

func (f *fakeLiveForge) CommitFiles(_ context.Context, _, _, message string, files map[string][]byte) (bool, error) {
	f.msgs = append(f.msgs, message)
	for p, c := range files {
		f.files[p] = c
		f.track(p)
	}
	return len(files) > 0, nil
}

// track adds a committed path to the tree, as a real forge does. A finalize
// that runs after a harvest reads a tree the harvest contributed to, and a
// fake that did not track this could not distinguish a harvest before a
// finalize from one after it (044b).
func (f *fakeLiveForge) track(path string) {
	if !slices.Contains(f.tree, path) {
		f.tree = append(f.tree, path)
	}
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) }
}

var testID = paper.Identity{Key: "arxiv:2603.01795", Title: "A Paper", Slug: "a-paper"}

// TestFinalizeLiveCleanTree checks AC6: a clean committed tree yields a
// manifest marked clear for redistribution and a committed transcript.
func TestFinalizeLiveCleanTree(t *testing.T) {
	forge := newFakeForge([]string{"README.md", "src/run.py", "results/plot.png"})
	events := []provider.Event{{Kind: "message", Content: "hello"}}

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, events, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	if len(rep.Leaked) != 0 {
		t.Errorf("a clean tree leaked %v", rep.Leaked)
	}
	if !rep.ManifestCommitted || rep.TranscriptEvents != 1 {
		t.Errorf("report = %+v, want a manifest + 1 transcript event", rep)
	}
	var m Manifest
	if err := json.Unmarshal(forge.files[ManifestFile], &m); err != nil {
		t.Fatalf("manifest not committed as JSON: %v", err)
	}
	if m.Redistribution.Review != ReviewClear || m.Redistribution.ThirdPartyData {
		t.Errorf("clean tree recorded %q/%v, want clear/false", m.Redistribution.Review, m.Redistribution.ThirdPartyData)
	}
	if m.Reproduction.Verdict != provider.GradePassed || m.Reproduction.Repo != "open-hai/a-paper-xyz" {
		t.Errorf("manifest reproduction = %+v", m.Reproduction)
	}
}

// TestFinalizeLiveLeakedTreeBarsPublic checks AC6: a tree carrying a
// third-party file, here a committed PDF, writes a manifest that is not clear,
// which bars the public flip, and still commits.
func TestFinalizeLiveLeakedTreeBarsPublic(t *testing.T) {
	forge := newFakeForge([]string{"README.md", "paper.pdf"})

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "leaky",
		testID, "sha256:abcd", provider.GradeFailed, nil, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	if len(rep.Leaked) != 1 || rep.Leaked[0] != "paper.pdf" {
		t.Fatalf("leaked = %v, want [paper.pdf]", rep.Leaked)
	}
	var m Manifest
	_ = json.Unmarshal(forge.files[ManifestFile], &m)
	if m.Redistribution.Review != ReviewExcluded || !m.Redistribution.ThirdPartyData {
		t.Errorf("leaked tree recorded %q/%v, want excluded/true (bars public)", m.Redistribution.Review, m.Redistribution.ThirdPartyData)
	}
}

// TestFinalizeLiveSecretInTranscriptBarsPublic checks 009 commit 1: a secret
// in the transcript, on an otherwise clean tree, writes the manifest with its
// own review reason, secret_in_transcript rather than third-party data, which
// bars the public flip. The transcript is still committed intact so the owner
// keeps the evidence.
func TestFinalizeLiveSecretInTranscriptBarsPublic(t *testing.T) {
	forge := newFakeForge([]string{"README.md", "src/run.py"})
	events := []provider.Event{
		{Kind: "message", Content: "starting the run"},
		{Kind: "tool_result", Content: "export TOKEN=AKIAIOSFODNN7EXAMPLE"},
	}

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "leaky-transcript",
		testID, "sha256:abcd", provider.GradePassed, events, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	if len(rep.Leaked) != 0 {
		t.Fatalf("the tree is clean; VerifyTree flagged %v", rep.Leaked)
	}
	if len(rep.TranscriptLeaks) != 1 {
		t.Fatalf("transcript leaks = %v, want exactly one", rep.TranscriptLeaks)
	}
	var m Manifest
	if err := json.Unmarshal(forge.files[ManifestFile], &m); err != nil {
		t.Fatalf("manifest not committed: %v", err)
	}
	if m.Redistribution.Review != ReviewSecretInTranscript {
		t.Errorf("review = %q, want %q", m.Redistribution.Review, ReviewSecretInTranscript)
	}
	if m.Redistribution.ThirdPartyData {
		t.Error("a transcript secret must not be reported as third-party data")
	}
	if ok, _ := EligibleForPublic(m); ok {
		t.Error("a reproduction whose transcript leaked must be ineligible for the public flip")
	}
	// The transcript is still committed. The reproduction stays readable to the
	// owner; only the public flip is refused.
	if !rep.TranscriptCommitted || rep.TranscriptFiles == 0 {
		t.Errorf("a flagged transcript must still be committed; report = %+v", rep)
	}
}

// TestFinalizeLiveIdempotentManifest checks that re-finalizing an unchanged
// reproduction does not rewrite the manifest.
func TestFinalizeLiveIdempotentManifest(t *testing.T) {
	forge := newFakeForge([]string{"README.md"})
	args := func() (FinalizeReport, error) {
		return FinalizeLive(context.Background(), forge, "open-hai", "r",
			testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	}
	if _, err := args(); err != nil {
		t.Fatal(err)
	}
	putsAfterFirst := forge.puts
	rep, err := args()
	if err != nil {
		t.Fatal(err)
	}
	if rep.ManifestCommitted {
		t.Error("second finalize should not re-commit an unchanged manifest")
	}
	if forge.puts != putsAfterFirst {
		t.Errorf("manifest was rewritten on a no-op finalize (puts %d -> %d)", putsAfterFirst, forge.puts)
	}
}

// TestRecordAndReadIdentity checks that RecordIdentity followed by
// RecordedIdentity round-trips the identity through the repository, so finalize
// never re-resolves over the paper source.
func TestRecordAndReadIdentity(t *testing.T) {
	forge := newFakeForge(nil)
	if err := RecordIdentity(context.Background(), forge, "open-hai", "r", testID); err != nil {
		t.Fatalf("RecordIdentity: %v", err)
	}
	got, ok, err := RecordedIdentity(context.Background(), forge, "open-hai", "r")
	if err != nil || !ok {
		t.Fatalf("RecordedIdentity ok=%v err=%v", ok, err)
	}
	if got.Key != testID.Key || got.Slug != testID.Slug {
		t.Errorf("round-trip identity = %+v, want %+v", got, testID)
	}
	// An absent identity reads back as not ok rather than as an error.
	if _, ok, err := RecordedIdentity(context.Background(), newFakeForge(nil), "o", "n"); ok || err != nil {
		t.Errorf("absent identity should be ok=false err=nil, got ok=%v err=%v", ok, err)
	}
}

// Findings (021a).

// goodFindings is a verdict.json that validates, so each test below can break
// exactly one thing about it.
func goodFindings(t *testing.T) []byte {
	t.Helper()
	v := verdict.Verdict{
		Schema:     verdict.Schema,
		Verdict:    verdict.Partial,
		Summary:    "The classifier retrains and matches within 1.8 pp.",
		GradedAt:   time.Date(2026, 7, 14, 10, 41, 0, 0, time.UTC),
		Components: verdict.Components{Verified: 1, Blocked: 1, Total: 2},
		Boundary: []verdict.Claim{
			{Claim: "Accuracy 91.4%", Loop: verdict.Inner, Outcome: verdict.Verified, Citation: "§6.1", Evidence: "89.6% retrained"},
			{Claim: "Latency under 40 ms", Loop: verdict.Inner, Outcome: verdict.Blocked, Citation: "§6.3", Evidence: "needs the authors' tablet"},
			{Claim: "Preferred by participants", Loop: verdict.Outer, Outcome: verdict.NotScored, Citation: "§7", Evidence: "human study"},
		},
		Decisions: []verdict.Decision{{Question: "Window length?", Where: "§4", Assumed: "250 ms", Sensitivity: "±0.4 pp"}},
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal findings: %v", err)
	}
	return raw
}

// TestFinalizeLiveRecordsValidFindings checks that a valid verdict.json is
// recorded on the manifest and that UNVERIFIED.md is left alone, since the
// agent's file is not touched when there is nothing to record.
func TestFinalizeLiveRecordsValidFindings(t *testing.T) {
	forge := newFakeForge([]string{"README.md", FindingsFile})
	forge.files[FindingsFile] = goodFindings(t)

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	if !rep.Findings {
		t.Fatalf("valid findings reported as unusable: %v", rep.FindingProblems)
	}
	// verdict.json must not read as third-party data. If it did, every new
	// reproduction would have a review other than clear and become
	// unpublishable, which would be a product-wide block introduced by adding
	// a file.
	if len(rep.Leaked) != 0 {
		t.Fatalf("the findings file tripped the redistribution gate: %v", rep.Leaked)
	}
	var m Manifest
	if err := json.Unmarshal(forge.files[ManifestFile], &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Findings == nil || !m.Findings.Present || len(m.Findings.Problems) != 0 {
		t.Fatalf("manifest findings = %+v, want present with no problems", m.Findings)
	}
	if _, ok := forge.files[UnverifiedFile]; ok {
		t.Error("a run with readable findings wrote to UNVERIFIED.md")
	}
}

// TestFinalizeLiveMissingFindingsStillPublishes checks the central decision:
// a missing file is recorded and the run still publishes. The manifest is the
// system's statement and must not depend on the agent's JSON.
func TestFinalizeLiveMissingFindingsStillPublishes(t *testing.T) {
	// A real reproduction in the tree with no verdict.json beside it. This is
	// the case the findings note is written for, as opposed to an empty
	// repository (044d).
	forge := newFakeForge([]string{"README.md", "REPRODUCIBILITY.md", "src/run.py"})

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	if err != nil {
		t.Fatalf("a missing verdict.json aborted the publish: %v", err)
	}
	if rep.Findings {
		t.Fatal("absent findings reported as present")
	}
	if !rep.ManifestCommitted {
		t.Error("the manifest was not committed")
	}
	var m Manifest
	_ = json.Unmarshal(forge.files[ManifestFile], &m)
	if m.Findings == nil || m.Findings.Present || len(m.Findings.Problems) == 0 {
		t.Fatalf("manifest findings = %+v, want absent with a reason", m.Findings)
	}
	note := string(forge.files[UnverifiedFile])
	if !strings.Contains(note, FindingsFile) || !strings.Contains(note, "(system)") {
		t.Errorf("UNVERIFIED.md does not attribute the note to the system: %q", note)
	}
	if !rep.Reproduction || !strings.Contains(note, "exist only as prose in this repository") {
		t.Errorf("a tree holding a reproduction was reported as empty: %q", note)
	}
}

// TestFinalizeLiveInvalidFindingsRecordsTheProblems checks that an invalid
// file records the specific schema problems rather than a generic failure.
func TestFinalizeLiveInvalidFindingsRecordsTheProblems(t *testing.T) {
	forge := newFakeForge([]string{"README.md", FindingsFile})
	var v verdict.Verdict
	_ = json.Unmarshal(goodFindings(t), &v)
	v.Boundary[2].Outcome = verdict.Verified // an outer-loop claim, scored
	raw, _ := json.Marshal(v)
	forge.files[FindingsFile] = raw

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	if err != nil {
		t.Fatalf("invalid findings aborted the publish: %v", err)
	}
	if rep.Findings {
		t.Fatal("an invalid verdict.json was accepted")
	}
	joined := strings.Join(rep.FindingProblems, "\n")
	if !strings.Contains(joined, "boundary[2].outcome") {
		t.Errorf("problems do not name the offending field: %q", joined)
	}
	if !strings.Contains(string(forge.files[UnverifiedFile]), "boundary[2].outcome") {
		t.Error("UNVERIFIED.md does not carry the specific problem")
	}
}

// TestFinalizeLiveNonJSONFindings checks that prose in the file is recorded as
// a parse failure and does not panic.
func TestFinalizeLiveNonJSONFindings(t *testing.T) {
	forge := newFakeForge([]string{"README.md", FindingsFile})
	forge.files[FindingsFile] = []byte("# Reproducibility\n\nPartially reproducible.\n")

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	if rep.Findings || len(rep.FindingProblems) == 0 {
		t.Fatalf("markdown was accepted as findings: %+v", rep)
	}
}

// TestFinalizeLiveDoesNotStackTheUnverifiedNote checks that re-finalizing
// converges: the note is rewritten and never duplicated.
func TestFinalizeLiveDoesNotStackTheUnverifiedNote(t *testing.T) {
	forge := newFakeForge([]string{"README.md"})
	forge.files[UnverifiedFile] = []byte("# Unverified\n\n- The dataset link 404s.\n")

	run := func() string {
		if _, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
			testID, "sha256:abcd", provider.GradePassed, nil, fixedNow()); err != nil {
			t.Fatalf("FinalizeLive: %v", err)
		}
		return string(forge.files[UnverifiedFile])
	}
	first := run()
	before := forge.puts
	second := run()

	if first != second {
		t.Errorf("re-finalize changed UNVERIFIED.md:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if forge.puts != before {
		t.Errorf("re-finalize rewrote UNVERIFIED.md (%d extra puts)", forge.puts-before)
	}
	if strings.Count(second, findingsHeading) != 1 {
		t.Errorf("the system note appears %d times", strings.Count(second, findingsHeading))
	}
	// The agent's own content survives beside it.
	if !strings.Contains(second, "The dataset link 404s.") {
		t.Error("appending the note destroyed the agent's own UNVERIFIED.md content")
	}
}

// An empty repository (044d).

// TestFinalizeLiveReportsARunThatCommittedNothing reproduces the incident's
// repository: the run committed nothing, so the tree holds only what the
// system put there before it started. Saying that the findings exist only as
// prose in this repository would promise prose that is not there.
func TestFinalizeLiveReportsARunThatCommittedNothing(t *testing.T) {
	forge := newFakeForge([]string{"README.md", IdentityFile})

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	if rep.Reproduction {
		t.Fatal("a tree holding only the system's own files reported a reproduction")
	}
	var m Manifest
	_ = json.Unmarshal(forge.files[ManifestFile], &m)
	if m.Findings == nil || m.Findings.Reproduction {
		t.Fatalf("manifest findings = %+v, want reproduction false", m.Findings)
	}
	note := string(forge.files[UnverifiedFile])
	if !strings.Contains(note, "committed nothing to this repository") {
		t.Errorf("UNVERIFIED.md does not say the repository is empty: %q", note)
	}
	if strings.Contains(note, "exist only as prose in this repository") {
		t.Errorf("UNVERIFIED.md promises prose that is not there: %q", note)
	}
}

// TestFinalizeLiveStillReportsAnEmptyRepositoryOnRefinalize checks that
// re-finalizing an empty repository reads a tree that now holds the manifest
// and the transcript, which are the system's own files and must not read as a
// reproduction, and converges rather than adding a second note.
func TestFinalizeLiveStillReportsAnEmptyRepositoryOnRefinalize(t *testing.T) {
	forge := newFakeForge([]string{"README.md", IdentityFile})
	events := []provider.Event{{Kind: "message", Content: "hello"}}
	first, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, events, fixedNow())
	if err != nil || first.Reproduction {
		t.Fatalf("first finalize = %+v, %v", first, err)
	}

	// The second pass sees everything the first one wrote. The fake tracks its
	// tree, so this is the same state a real re-finalize meets.
	second, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, events, fixedNow())
	if err != nil {
		t.Fatalf("re-finalize: %v", err)
	}
	if second.Reproduction {
		t.Error("the system's own files read as a reproduction on re-finalize")
	}
	note := string(forge.files[UnverifiedFile])
	if n := strings.Count(note, findingsHeading); n != 1 {
		t.Errorf("the system note appears %d times, want once:\n%s", n, note)
	}
}

// TestHoldsReproduction checks that one agent-authored path is enough. The
// question the system can answer is whether a reproduction exists, not whether
// it is good.
func TestHoldsReproduction(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree []string
		want bool
	}{
		{"empty repository", []string{"README.md", IdentityFile}, false},
		{"only the system's files", []string{"README.md", IdentityFile, ManifestFile,
			UnverifiedFile, TranscriptMD, TranscriptJSONL, transcriptDir + "/001.md"}, false},
		{"nothing at all", nil, false},
		{"a report", []string{"README.md", "REPRODUCIBILITY.md"}, true},
		{"source only", []string{"src/run.py"}, true},
		{"findings only", []string{FindingsFile}, true},
		{"a file that merely starts like the transcript dir", []string{"transcripts.md"}, true},
	} {
		if got := holdsReproduction(tc.tree); got != tc.want {
			t.Errorf("%s: holdsReproduction(%v) = %v, want %v", tc.name, tc.tree, got, tc.want)
		}
	}
}

// A repository with no commit at all is the run that pushed nothing, and
// finalize's job is to say so. It used to ask for the head, get an error, and
// return it — so the record 044d exists to write was the one case that could
// not be written, and the terminal reported a 404 against a repository it had
// just created.
func TestFinalizeRecordsARepositoryWithNoCommit(t *testing.T) {
	forge := newFakeForge(nil)
	forge.head = ""
	id := paper.Identity{Key: "doi:10.1145/1", Title: "A paper", Scheme: paper.SchemeDOI}

	rep, err := FinalizeLive(t.Context(), forge, "open-hai", "a-paper-a3f9", id,
		"sha256:method", provider.GradePassed, nil, func() time.Time { return time.Unix(0, 0).UTC() })
	if err != nil {
		t.Fatalf("an empty repository stopped the record being written: %v", err)
	}
	if rep.Head != "" {
		t.Errorf("head = %q, want empty", rep.Head)
	}
	if rep.Reproduction {
		t.Error("a repository with no commit holds no reproduction")
	}
	if len(rep.Leaked) != 0 {
		t.Errorf("nothing was committed, so nothing can leak: %v", rep.Leaked)
	}
	if !rep.ManifestCommitted {
		t.Error("the manifest that records the emptiness was not committed")
	}
	// And the manifest says it in the field a reader checks, not only in prose.
	raw, ok := forge.files[ManifestFile]
	if !ok {
		t.Fatal("no manifest was written")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Findings == nil || m.Findings.Reproduction {
		t.Errorf("the manifest claims a reproduction that was never pushed: %+v", m.Findings)
	}
}

// TestFinalizeLiveNamesTheRowThatDidNotDecode checks that one unreadable row
// is recorded as a problem at that row. It used to be recorded as the whole
// file not being JSON, which sent a reader of UNVERIFIED.md looking for a
// syntax error that was not there.
func TestFinalizeLiveNamesTheRowThatDidNotDecode(t *testing.T) {
	forge := newFakeForge([]string{"README.md", FindingsFile})
	var doc map[string]any
	if err := json.Unmarshal(goodFindings(t), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["boundary"].([]any)[1] = "Latency under 40 ms"
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	forge.files[FindingsFile] = raw

	rep, err := FinalizeLive(context.Background(), forge, "open-hai", "a-paper-xyz",
		testID, "sha256:abcd", provider.GradePassed, nil, fixedNow())
	if err != nil {
		t.Fatalf("FinalizeLive: %v", err)
	}
	joined := strings.Join(rep.FindingProblems, "\n")
	if rep.Findings || !strings.Contains(joined, "boundary[1]") || strings.Contains(joined, "not valid JSON") {
		t.Fatalf("problems = %q, want the row named and no claim about the JSON", joined)
	}
}

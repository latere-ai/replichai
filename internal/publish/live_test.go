package publish

import (
	"context"
	"encoding/json"
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
	puts  int // PutFile calls
}

func newFakeForge(tree []string) *fakeLiveForge {
	return &fakeLiveForge{head: "deadbeef", tree: tree, files: map[string][]byte{}}
}

func (f *fakeLiveForge) HeadCommit(context.Context, string, string) (string, error) {
	return f.head, nil
}

func (f *fakeLiveForge) TreePaths(context.Context, string, string, string) ([]string, error) {
	return f.tree, nil
}

func (f *fakeLiveForge) GetFileContent(_ context.Context, _, _, path string) ([]byte, bool, error) {
	b, ok := f.files[path]
	return b, ok, nil
}

func (f *fakeLiveForge) PutFile(_ context.Context, _, _, path, _ string, content []byte) error {
	f.files[path] = content
	f.puts++
	return nil
}

func (f *fakeLiveForge) CommitFiles(_ context.Context, _, _, _ string, files map[string][]byte) (bool, error) {
	for p, c := range files {
		f.files[p] = c
	}
	return len(files) > 0, nil
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) }
}

var testID = paper.Identity{Key: "arxiv:2603.01795", Title: "A Paper", Slug: "a-paper"}

// AC6: a clean committed tree yields a manifest marked redistribution-clear and
// a committed transcript.
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

// AC6: a tree carrying a third-party file (a committed PDF) writes the manifest
// NOT clear — barring the public flip — and still commits.
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

// 009 commit 1: a secret in the transcript, on an otherwise-clean tree, writes
// the manifest with its own review reason (secret_in_transcript, not
// third-party data) — barring the public flip — and still commits the
// transcript intact so the owner keeps the evidence.
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
	// The transcript is still committed — the reproduction stays readable to the
	// owner; only the public flip is refused.
	if !rep.TranscriptCommitted || rep.TranscriptFiles == 0 {
		t.Errorf("a flagged transcript must still be committed; report = %+v", rep)
	}
}

// Re-finalizing an unchanged reproduction rewrites no manifest — idempotent.
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

// RecordIdentity then RecordedIdentity round-trips the identity through the repo,
// so finalize never re-resolves over the paper source.
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
	// An absent identity reads back not-ok, not an error.
	if _, ok, err := RecordedIdentity(context.Background(), newFakeForge(nil), "o", "n"); ok || err != nil {
		t.Errorf("absent identity should be ok=false err=nil, got ok=%v err=%v", ok, err)
	}
}

// ── findings (021a) ──────────────────────────────────────────────────────────

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

// A valid verdict.json is recorded on the manifest and leaves UNVERIFIED.md
// alone — the agent's file is not touched when there is nothing to say.
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

// The load-bearing decision: a missing file is recorded and the run still
// publishes. The manifest is the system's word and must not depend on the
// agent's JSON.
func TestFinalizeLiveMissingFindingsStillPublishes(t *testing.T) {
	forge := newFakeForge([]string{"README.md"})

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
}

// An invalid file records the specific schema problems, not "invalid".
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

// Prose in the file is a parse failure, not a panic, and is recorded as one.
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

// Re-finalizing must converge: the note is rewritten, never stacked.
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

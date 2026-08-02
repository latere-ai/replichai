package publish

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
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

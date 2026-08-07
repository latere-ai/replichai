package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// IdentityFile records the resolved paper identity in a published repository, so
// the finalize step reads it from here rather than re-resolving over the slow,
// rate-limited paper source. It matches the CLI's recorded location.
const IdentityFile = ".replichai/paper.json"

// LiveForge is the read+write seam for finalizing a live-mount reproduction: the
// agent has already committed its own work into the mounted repository, and
// finalize reads the committed tree and adds the system's manifest and
// transcript beside it. github.Client satisfies it; a fake stands in for tests,
// so this path needs no network. It is deliberately owner+name addressed (unlike
// the org-fixed Forge of the snapshot path) because it acts on a repository that
// already exists under a known owner.
type LiveForge interface {
	// HeadCommit returns the repository's current head commit sha.
	HeadCommit(ctx context.Context, owner, name string) (string, error)
	// TreePaths lists every path committed at a commit.
	TreePaths(ctx context.Context, owner, name, commit string) ([]string, error)
	// GetFileContent reads one file; ok is false when it is absent.
	GetFileContent(ctx context.Context, owner, name, path string) ([]byte, bool, error)
	// PutFile writes (or overwrites) one file in a single commit.
	PutFile(ctx context.Context, owner, name, path, message string, content []byte) error
	// CommitFiles writes a set of files in one atomic commit, reporting whether
	// anything changed (a no-op when the content already matches).
	CommitFiles(ctx context.Context, owner, name, message string, files map[string][]byte) (bool, error)
}

// FinalizeReport is what a finalize produced, so a caller (the CLI) can report
// it without the finalize owning any output. Leaked is the third-party paths the
// verifier found in the committed tree — non-empty bars the public flip.
type FinalizeReport struct {
	Head              string
	Leaked            []string
	ManifestCommitted bool
	// Transcript* are set when events were supplied. TranscriptCommitted is false
	// when the transcript was already current (a re-finalize adds no commit).
	TranscriptCommitted bool
	TranscriptFiles     int
	TranscriptEvents    int
	TranscriptLeaks     []TranscriptLeak
}

// RecordIdentity writes the resolved paper identity into the repository, so a
// later finalize reads it from here instead of re-resolving. Best-effort at the
// call site: a failure is returned, and the caller decides whether to proceed.
func RecordIdentity(ctx context.Context, forge LiveForge, owner, name string, id paper.Identity) error {
	raw, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	_, err = forge.CommitFiles(ctx, owner, name, "chore: record paper identity",
		map[string][]byte{IdentityFile: append(raw, '\n')})
	return err
}

// RecordedIdentity returns the identity RecordIdentity wrote, with ok=false when
// none is present (an older or hand-created repository) so the caller falls back
// to re-resolving. An unreadable file is treated as absent, never an error.
func RecordedIdentity(ctx context.Context, forge LiveForge, owner, name string) (id paper.Identity, ok bool, err error) {
	raw, present, err := forge.GetFileContent(ctx, owner, name, IdentityFile)
	if err != nil || !present {
		return paper.Identity{}, false, err
	}
	if json.Unmarshal(raw, &id) != nil {
		return paper.Identity{}, false, nil
	}
	return id, true, nil
}

// FinalizeLive finalizes a live-mount reproduction: it reads the committed tree,
// writes replication.json (only when new or changed), and commits the transcript
// beside it — the system-authored attestation over the agent's own work. It
// touches no paper source and no clock beyond `now`, so both the CLI and the
// server drive one finalize and cannot drift.
func FinalizeLive(ctx context.Context, forge LiveForge, owner, name string, id paper.Identity, methodDigest string, verdict provider.Grade, events []provider.Event, now func() time.Time) (FinalizeReport, error) {
	if now == nil {
		now = time.Now
	}
	rep := FinalizeReport{}

	head, err := forge.HeadCommit(ctx, owner, name)
	if err != nil {
		return rep, fmt.Errorf("read head commit: %w", err)
	}
	rep.Head = head

	tree, err := forge.TreePaths(ctx, owner, name, head)
	if err != nil {
		return rep, fmt.Errorf("read committed tree: %w", err)
	}
	rep.Leaked = VerifyTree(tree)

	// The transcript is a published surface too (005): a secret in it bars the
	// public flip. Scan before the manifest and fold the result into the
	// redistribution classification, so the manifest records — with its own
	// review reason — that a foreign credential in the evidence layer, not just a
	// leaked file in the tree, keeps the reproduction private (009 commit 1).
	rep.TranscriptLeaks = TranscriptLeaks(events)
	redist := RedistributionFrom(len(rep.Leaked) == 0, len(rep.TranscriptLeaks) == 0)
	var transcriptFiles map[string][]byte
	if len(events) > 0 {
		transcriptFiles, err = RenderTranscriptFiles(events)
		if err != nil {
			return rep, fmt.Errorf("render transcript: %w", err)
		}
	}

	manifest := ManifestFor(id, owner+"/"+name, methodDigest, verdict, now(), redist)
	committed, err := commitManifestIfChanged(ctx, forge, owner, name, manifest)
	if err != nil {
		return rep, err
	}
	rep.ManifestCommitted = committed

	// A flagged transcript is still committed intact — the reproduction stays
	// readable to the owner and collaborators; only the public flip is refused.
	if len(events) > 0 {
		changed, err := forge.CommitFiles(ctx, owner, name, "chore: reproduction transcript", transcriptFiles)
		if err != nil {
			return rep, fmt.Errorf("commit transcript: %w", err)
		}
		rep.TranscriptCommitted = changed
		rep.TranscriptFiles = len(transcriptFiles)
		rep.TranscriptEvents = len(events)
	}
	return rep, nil
}

// commitManifestIfChanged writes replication.json only when it is new or
// materially different, so re-finalizing the same reproduction adds no redundant
// commit. It reports whether it committed.
func commitManifestIfChanged(ctx context.Context, forge LiveForge, owner, name string, m Manifest) (bool, error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	if existing, ok, err := forge.GetFileContent(ctx, owner, name, ManifestFile); err != nil {
		return false, err
	} else if ok {
		var prev Manifest
		if json.Unmarshal(existing, &prev) == nil && SameManifest(prev, m) {
			return false, nil // unchanged — no commit
		}
	}
	if err := forge.PutFile(ctx, owner, name, ManifestFile,
		"chore: reproduction manifest", append(raw, '\n')); err != nil {
		return false, fmt.Errorf("commit manifest: %w", err)
	}
	return true, nil
}

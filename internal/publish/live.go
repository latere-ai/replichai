package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/verdict"
)

// IdentityFile records the resolved paper identity in a published repository, so
// the finalize step reads it from here rather than re-resolving over the slow,
// rate-limited paper source. It matches the CLI's recorded location.
const IdentityFile = ".replichai/paper.json"

// FindingsFile is the agent-authored verdict.json (021a) and UnverifiedFile is
// the agent's own record of what it could not confirm — the right place to say
// that its findings could not be read, because that is exactly what that file
// is for.
const (
	FindingsFile   = verdict.File
	UnverifiedFile = "UNVERIFIED.md"
)

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
	// Findings reports whether verdict.json was present and valid (021a).
	// FindingProblems carries why not, and is what UNVERIFIED.md records.
	Findings        bool
	FindingProblems []string
	// Reproduction reports whether the committed tree holds anything the agent
	// put there. False means the run committed nothing: a different and more
	// serious fact than unreadable findings, and one the record used to state as
	// the other (044d).
	Reproduction bool
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
		return paper.Identity{}, false, nil //nolint:nilerr // an unreadable record is absent, not an error: the caller re-resolves
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

	// The agent's findings are read, validated, and recorded — never enforced.
	// A run whose verdict.json is missing or malformed still publishes; what
	// changes is that the manifest says so and UNVERIFIED.md gains the reason.
	findings, problems := readFindings(ctx, forge, owner, name)
	rep.Findings = findings
	rep.FindingProblems = problems

	// Whether the run committed anything at all is a separate question from
	// whether its findings are readable, and the same tree answers it. A run that
	// pushed nothing used to be reported as one whose findings were merely not
	// machine-readable — a sentence promising prose that is not there (044d).
	rep.Reproduction = holdsReproduction(tree)

	manifest := ManifestFor(id, owner+"/"+name, methodDigest, verdict, now(), redist)
	manifest.Findings = &Findings{Present: findings, Problems: problems, Reproduction: rep.Reproduction}
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

	if !findings {
		if err := noteUnverified(ctx, forge, owner, name, problems, rep.Reproduction); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// readFindings reads verdict.json from the committed tree and validates it.
// It reports whether usable findings are present, and if not, why — an absent
// file, unparseable JSON, or the specific schema problems, each phrased so the
// reason survives into the manifest and into UNVERIFIED.md unchanged.
func readFindings(ctx context.Context, forge LiveForge, owner, name string) (bool, []string) {
	raw, present, err := forge.GetFileContent(ctx, owner, name, FindingsFile)
	if err != nil {
		return false, []string{"could not be read: " + err.Error()}
	}
	if !present {
		return false, []string{"the run did not write " + FindingsFile}
	}
	problems, err := verdict.Validate(raw)
	if err != nil {
		return false, []string{err.Error()}
	}
	if len(problems) == 0 {
		return true, nil
	}
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.String())
	}
	return false, out
}

// noteUnverified appends the reason the findings could not be read to the
// agent's own UNVERIFIED.md, under a heading that names the system as the
// author — the file is the agent's, and a system line inside it has to be
// attributable or it reads as something the agent claimed.
func noteUnverified(ctx context.Context, forge LiveForge, owner, name string, problems []string, reproduction bool) error {
	existing, _, err := forge.GetFileContent(ctx, owner, name, UnverifiedFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", UnverifiedFile, err)
	}
	// Re-finalizing must not stack the same note. The previous system section is
	// removed before the current one is appended, so the file converges rather
	// than growing a paragraph per finalize.
	body := strings.TrimRight(withoutFindingsNote(string(existing)), "\n")

	var b strings.Builder
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(findingsHeading + "\n\n")
	if reproduction {
		b.WriteString("`" + FindingsFile + "` could not be read, so this reproduction's boundary table, hidden decisions, and scorecard exist only as prose in this repository:\n\n")
	} else {
		b.WriteString("This run committed nothing to this repository. There is no reproduction here — no report, no source, and no `" + FindingsFile +
			"` — and the session transcript beside this file is the only record of what the run did:\n\n")
	}
	for _, p := range problems {
		b.WriteString("- " + p + "\n")
	}
	next := b.String()
	if next == string(existing) {
		return nil // already says exactly this
	}
	if err := forge.PutFile(ctx, owner, name, UnverifiedFile,
		"chore: record that the findings could not be read", []byte(next)); err != nil {
		return fmt.Errorf("record unverified findings: %w", err)
	}
	return nil
}

// findingsHeading marks the system's own section inside the agent's file. It is
// attributed because a system line in an agent-authored document otherwise
// reads as something the agent claimed.
const findingsHeading = "## Findings not recorded as data (system)"

// withoutFindingsNote drops a previously written system section, which runs to
// the end of the file — it is always appended last.
func withoutFindingsNote(s string) string {
	if before, _, ok := strings.Cut(s, findingsHeading); ok {
		return before
	}
	return s
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

// outputsMessage names a harvest in the history, so a reader can tell files the
// system recovered from files the agent pushed itself.
const outputsMessage = "chore: reproduction outputs"

// OutputsReport is what a harvest did with each path it was handed, so the
// caller can say what it recovered and what it would not carry.
type OutputsReport struct {
	// Committed is what landed, in path order.
	Committed []string
	// Refused is what VerifyTree flagged, or what named an unsafe path. These
	// are never committed: a harvested file has not passed the mount's
	// redistribution boundary, so it passes the same gate on the way in that the
	// committed tree passes on the way out (006f, 009).
	Refused []string
	// Skipped is what the committed tree already holds. A pushed file is the
	// agent's considered work; a copy in the output directory may predate its
	// last revision, so what the agent published wins.
	Skipped []string
}

// Recovered reports whether the harvest put anything into the repository.
func (r OutputsReport) Recovered() bool { return len(r.Committed) > 0 }

// CommitOutputs commits what a session produced outside the mounted repository
// — the files it wrote to the runtime's output directory — into the repository,
// in one commit (044b).
//
// It exists because an agent may write its reproduction, copy it out, and never
// push: the mount is the only surface that survives the sandbox, and a run that
// committed locally and never pushed leaves a repository holding nothing. The
// output directory is the second copy, and this is what reads it.
//
// It never overwrites what the agent pushed, and it commits nothing VerifyTree
// flags. Idempotent: re-running it over a repository that already holds these
// files commits nothing, because every path is then already in the tree.
func CommitOutputs(ctx context.Context, forge LiveForge, owner, name string, files map[string][]byte) (OutputsReport, error) {
	var rep OutputsReport
	if len(files) == 0 {
		return rep, nil
	}
	head, err := forge.HeadCommit(ctx, owner, name)
	if err != nil {
		return rep, fmt.Errorf("read head commit: %w", err)
	}
	tree, err := forge.TreePaths(ctx, owner, name, head)
	if err != nil {
		return rep, fmt.Errorf("read committed tree: %w", err)
	}
	present := make(map[string]bool, len(tree))
	for _, p := range tree {
		present[p] = true
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	clean := make(map[string][]byte, len(paths))
	for _, p := range paths {
		normal, ok := safeRepoPath(p)
		if !ok || len(VerifyTree([]string{normal})) > 0 {
			rep.Refused = append(rep.Refused, p)
			continue
		}
		if present[normal] {
			rep.Skipped = append(rep.Skipped, normal)
			continue
		}
		clean[normal] = files[p]
		rep.Committed = append(rep.Committed, normal)
	}
	if len(clean) == 0 {
		return rep, nil
	}
	if _, err := forge.CommitFiles(ctx, owner, name, outputsMessage, clean); err != nil {
		return rep, fmt.Errorf("commit outputs: %w", err)
	}
	return rep, nil
}

// safeRepoPath normalizes a path handed in from outside the repository and
// reports whether it may be committed at all. An absolute path, a path that
// climbs out of the tree, and anything under the repository's own .git are
// refused rather than cleaned into something that looks harmless.
func safeRepoPath(p string) (string, bool) {
	s := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if s == "" || strings.HasPrefix(s, "/") {
		return "", false
	}
	s = path.Clean(s)
	if s == "." || s == ".." || strings.HasPrefix(s, "../") {
		return "", false
	}
	if s == ".git" || strings.HasPrefix(s, ".git/") {
		return "", false
	}
	return s, true
}

// systemAuthored is every path this repository's own machinery writes: the
// recorded identity, the manifest, the transcript, the note appended to the
// agent's UNVERIFIED.md, and the README GitHub creates the repository with.
// Everything else in a committed tree came from the run.
func systemAuthored(p string) bool {
	switch p {
	case IdentityFile, ManifestFile, UnverifiedFile, TranscriptMD, TranscriptJSONL, "README.md":
		return true
	}
	return strings.HasPrefix(p, transcriptDir+"/")
}

// holdsReproduction reports whether a committed tree holds anything the run put
// there. It is deliberately generous about what counts — one agent-authored path
// is enough — because the question it answers is not "is this reproduction any
// good" but "is there one at all", and only the second has an answer the system
// can give.
func holdsReproduction(tree []string) bool {
	for _, p := range tree {
		if !systemAuthored(p) {
			return true
		}
	}
	return false
}

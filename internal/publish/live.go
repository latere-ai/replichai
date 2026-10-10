// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"latere.ai/x/pkg/relpath"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/verdict"
)

// IdentityFile records the resolved paper identity in a published repository, so
// the finalize step reads it from here rather than re-resolving over the slow,
// rate-limited paper source. It matches the CLI's recorded location.
const IdentityFile = ".replichai/paper.json"

// FindingsFile is the agent-authored verdict.json (021a). UnverifiedFile is
// the agent's own record of what it could not confirm, which is the correct
// place to record that its findings could not be read.
const (
	FindingsFile   = verdict.File
	UnverifiedFile = "UNVERIFIED.md"
)

// LiveForge is the read and write interface for finalizing a live-mount
// reproduction. The agent has already committed its own work into the mounted
// repository, and finalize reads the committed tree and adds the system's
// manifest and transcript beside it. github.Client satisfies it, and a fake
// replaces it in tests, so this path needs no network. It is addressed by
// owner and name, unlike the Forge of the snapshot path, which has a fixed
// organization, because it acts on a repository that already exists under a
// known owner.
type LiveForge interface {
	// HeadCommit returns the repository's current head commit sha, or an empty
	// string when the repository holds no commit at all.
	//
	// Empty is a state, not a failure. A repository is created with no commit
	// so the host's push can be its first (045g), and a run that pushed
	// nothing leaves it that way — which is exactly the outcome 044d exists to
	// record. An adapter that reported it as an error made finalize abort
	// before it could write down what happened, so the one case the record was
	// built for was the one it could not state.
	HeadCommit(ctx context.Context, owner, name string) (string, error)
	// TreePaths lists every path committed at a commit.
	TreePaths(ctx context.Context, owner, name, commit string) ([]string, error)
	// GetFileContent reads one file; ok is false when it is absent.
	GetFileContent(ctx context.Context, owner, name, path string) ([]byte, bool, error)
	// PutFile writes or overwrites one file in a single commit.
	PutFile(ctx context.Context, owner, name, path, message string, content []byte) error
	// CommitFiles writes a set of files in one atomic commit and reports whether
	// anything changed. It is a no-op when the content already matches.
	CommitFiles(ctx context.Context, owner, name, message string, files map[string][]byte) (bool, error)
}

// FinalizeReport is what a finalize produced, so a caller such as the CLI can
// report it without the finalize writing any output. Leaked lists the
// third-party paths the verifier found in the committed tree; a non-empty list
// bars the public flip.
type FinalizeReport struct {
	Head              string
	Leaked            []string
	ManifestCommitted bool
	// Transcript* are set when events were supplied. TranscriptCommitted is false
	// when the transcript was already current, since a re-finalize adds no
	// commit.
	TranscriptCommitted bool
	TranscriptFiles     int
	TranscriptEvents    int
	TranscriptLeaks     []TranscriptLeak
	// Findings reports whether verdict.json was present and valid (021a).
	// FindingProblems carries why not, and is what UNVERIFIED.md records.
	Findings        bool
	FindingProblems []string
	// Reproduction reports whether the committed tree holds anything the agent
	// put there. False means the run committed nothing, which is a different
	// and more serious fact than unreadable findings, and one the record once
	// reported as the other (044d).
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

// RecordedIdentity returns the identity RecordIdentity wrote, with ok false
// when none is present, as in an older or hand-created repository, so the
// caller falls back to re-resolving. An unreadable file is treated as absent
// rather than as an error.
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

// FinalizeLive finalizes a live-mount reproduction. It reads the committed
// tree, writes replication.json when it is new or changed, and commits the
// transcript beside it. These are the system-authored attestation over the
// agent's own work. It reads no paper source and no clock other than now, so
// the CLI and the server share one finalize and cannot drift.
//
// run is what the caller knows about the run itself. A caller that knows
// nothing passes the zero Run, and the manifest then keeps whatever record an
// earlier finalize wrote: the run happened once, and a later finalize that did
// not see it must not erase the account of it.
func FinalizeLive(ctx context.Context, forge LiveForge, owner, name string, id paper.Identity, methodDigest string, verdict provider.Grade, run Run, events []provider.Event, now func() time.Time) (FinalizeReport, error) {
	if now == nil {
		now = time.Now
	}
	rep := FinalizeReport{}

	head, err := forge.HeadCommit(ctx, owner, name)
	if err != nil {
		return rep, fmt.Errorf("read head commit: %w", err)
	}
	rep.Head = head

	// A repository with no commit has no tree to read, and asking for one is
	// how this used to fail instead of recording 044d's outcome. Everything
	// below reads an empty tree correctly: nothing leaked, no findings, no
	// reproduction — which is the true account of a run that pushed nothing.
	var tree []string
	if head != "" {
		tree, err = forge.TreePaths(ctx, owner, name, head)
		if err != nil {
			return rep, fmt.Errorf("read committed tree: %w", err)
		}
	}
	rep.Leaked = VerifyTree(tree)

	// The transcript is also published (005), and a secret in it bars the
	// public flip. It is scanned before the manifest is built and the result is
	// folded into the redistribution classification, so the manifest records,
	// under its own review reason, that a credential in the transcript keeps
	// the reproduction private, and not only a leaked file in the tree (009
	// commit 1).
	rep.TranscriptLeaks = TranscriptLeaks(events)
	redist := RedistributionFrom(len(rep.Leaked) == 0, len(rep.TranscriptLeaks) == 0)
	var transcriptFiles map[string][]byte
	if len(events) > 0 {
		transcriptFiles, err = RenderTranscriptFiles(events)
		if err != nil {
			return rep, fmt.Errorf("render transcript: %w", err)
		}
	}

	// The agent's findings are read, validated, and recorded, and never
	// enforced. A run whose verdict.json is missing or malformed still
	// publishes; the manifest records the fact and UNVERIFIED.md gains the
	// reason.
	findings, problems := readFindings(ctx, forge, owner, name)
	rep.Findings = findings
	rep.FindingProblems = problems

	// Whether the run committed anything is a separate question from whether
	// its findings are readable, and the same tree answers it. A run that
	// pushed nothing was once reported as one whose findings were not
	// machine-readable, which promised prose that was not there (044d).
	rep.Reproduction = holdsReproduction(tree)

	manifest := ManifestFor(id, owner+"/"+name, methodDigest, verdict, now(), redist)
	manifest.Findings = &Findings{Present: findings, Problems: problems, Reproduction: rep.Reproduction}
	if run.known() {
		if run.Rounds == 0 {
			run.Rounds = gradingRounds(tree)
		}
		manifest.Run = &run
	}
	committed, err := commitManifestIfChanged(ctx, forge, owner, name, manifest)
	if err != nil {
		return rep, err
	}
	rep.ManifestCommitted = committed

	// A flagged transcript is still committed intact. The reproduction stays
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
// It reports whether usable findings are present and, if not, why: an absent
// file, a file that is not a JSON object, or the specific schema problems,
// which name each field or row that could not be read as well as each broken
// rule. Each reason is phrased so it can be copied into the manifest and into
// UNVERIFIED.md unchanged.
func readFindings(ctx context.Context, forge LiveForge, owner, name string) (bool, []string) {
	raw, present, err := forge.GetFileContent(ctx, owner, name, FindingsFile)
	if err != nil {
		return false, []string{"could not be read: " + err.Error()}
	}
	if !present {
		return false, []string{"the run did not write " + FindingsFile}
	}
	_, problems, err := verdict.Validate(raw)
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
// author. The file is the agent's, and a system line inside it has to be
// attributed or it reads as something the agent claimed.
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
// the end of the file because it is always appended last.
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
		if json.Unmarshal(existing, &prev) == nil {
			// What an earlier finalize recorded about the run is settled with
			// what this one knows, so a finalize that knows less erases nothing.
			if merged := mergeRun(prev.Run, m.Run); merged != m.Run {
				m.Run = merged
				if raw, err = json.MarshalIndent(m, "", "  "); err != nil {
					return false, err
				}
			}
			if SameManifest(prev, m) {
				return false, nil // unchanged, so no commit
			}
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
	// Committed is what was committed, in path order.
	Committed []string
	// Refused is what VerifyTree flagged, or what named an unsafe path. These
	// are never committed: a harvested file has not passed the mount's
	// redistribution boundary, so it passes the same gate on the way in that the
	// committed tree passes on the way out (006f, 009).
	Refused []string
	// Skipped is what the committed tree already holds. A pushed file is the
	// agent's considered work; a copy in the output directory may predate its
	// last revision, so the agent's pushed copy takes precedence.
	Skipped []string
}

// Recovered reports whether the harvest put anything into the repository.
func (r OutputsReport) Recovered() bool { return len(r.Committed) > 0 }

// CommitOutputs commits the files a session wrote to the runtime's output
// directory, outside the mounted repository, into the repository in one commit
// (044b).
//
// It exists because an agent may write its reproduction, copy it out, and
// never push. The mount is the only surface that survives the sandbox, and a
// run that committed locally and never pushed leaves a repository holding
// nothing. The output directory is the second copy, and CommitOutputs reads
// it.
//
// It never overwrites what the agent pushed, and it commits nothing VerifyTree
// flags. It is idempotent: re-running it over a repository that already holds
// these files commits nothing, because every path is then already in the
// tree.
func CommitOutputs(ctx context.Context, forge LiveForge, owner, name string, files map[string][]byte) (OutputsReport, error) {
	var rep OutputsReport
	if len(files) == 0 {
		return rep, nil
	}
	head, err := forge.HeadCommit(ctx, owner, name)
	if err != nil {
		return rep, fmt.Errorf("read head commit: %w", err)
	}
	// An empty repository is the case this recovery is most needed in: the
	// agent's push is exactly what did not happen, so there is no tree to read
	// and nothing of the agent's that these files could overwrite.
	var tree []string
	if head != "" {
		tree, err = forge.TreePaths(ctx, owner, name, head)
		if err != nil {
			return rep, fmt.Errorf("read committed tree: %w", err)
		}
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
	s, err := relpath.Clean(s)
	if err != nil || s == "." {
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

// mergeRun settles what the manifest says about the run when an earlier
// finalize has already said something. A run is finalized more than once: when
// it ends, again by `replichai finish`, again by a recovery path, and each of
// those knows a different amount.
//
//   - A finalize that knows nothing keeps the earlier record.
//   - A finalize of the same session fills what it does not know from the
//     earlier record and replaces what it does. The model that was asked for
//     and the prompt are fixed when a session starts, so only the process that
//     started it knows them, and a later finish must not strip them.
//   - A record with no session, which is an attempt that never started, leaves
//     the record of a session that did run. The repository still holds that
//     session's work, and its record is the account of it.
//   - A different session replaces the record whole. Mixing two sessions'
//     facts would describe a run that never happened.
func mergeRun(prev, next *Run) *Run {
	switch {
	case next == nil:
		return prev
	case prev == nil:
		return next
	case next.Session == "" && prev.Session != "":
		return prev
	case next.Session != prev.Session:
		return next
	}
	merged := *next
	earlier, settled := reflect.ValueOf(prev).Elem(), reflect.ValueOf(&merged).Elem()
	for i := range settled.NumField() {
		if settled.Field(i).IsZero() {
			settled.Field(i).Set(earlier.Field(i))
		}
	}
	return &merged
}

// gradingRounds counts the grades the tree carries. A local run commits each
// round's grade as grade/verdict-<n>.json beside its work, so the count is how
// many times the work was graded. A tree with none answers zero, which the
// manifest leaves out: a hosted runtime grades out of sight, and that is not
// the same as never grading.
func gradingRounds(tree []string) int {
	rounds := 0
	for _, p := range tree {
		if strings.HasPrefix(p, gradeDir+"/verdict-") && strings.HasSuffix(p, ".json") {
			rounds++
		}
	}
	return rounds
}

// holdsReproduction reports whether a committed tree holds anything the run put
// there. One agent-authored path is enough, because the question it answers is
// whether a reproduction exists and not whether it is good, and only the first
// has an answer the system can give.
func holdsReproduction(tree []string) bool {
	for _, p := range tree {
		if !systemAuthored(p) {
			return true
		}
	}
	return false
}

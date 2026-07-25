# 009 — Secret containment when the agent commits live

**Status:** deferred (backlog — revisit after the MVP)
**Scope:** what happens when a secret reaches a place we publish — the committed tree, the git history, or the transcript — given that the agent commits and pushes while it works.
**Depends on:** [002](002-publishing-reproductions.md) (the gate), [005](005-transcript.md) (the transcript), [006f](006f-taking-a-reproduction-out.md) (the mount and the public flip).

## Why deferred

Nothing today makes a reproduction public. Repositories are created private, stay private, and are shared only by inviting a named collaborator. So a leak is currently contained by the fact that there is no publication step to leak *through* — the exposure is to the organisation and the invited owner, not the world. That is what makes this a backlog item rather than a hole: the containment is real but structural, not enforced, and the moment the visibility flip is wired it stops being enough.

## Problem

The mount path (006f) is the real publish path, and it removed the place where filtering used to happen. The agent commits and pushes into a live repository as it works; by the time the system sees anything, it is already pushed. Three surfaces can carry a secret:

- **the committed tree** — a `.env` the agent wrote, a credential inside something it downloaded;
- **the git history** — the same file in an earlier commit, still reachable even if a later commit deletes it;
- **the transcript** — the backend's event log, which records the agent's shell, so an `export` or an echoed token is in the evidence layer by construction.

The third is the awkward one. The transcript is evidence precisely because the system fetches it from the backend rather than letting the agent write its own account. Masking it weakens the thing that makes it worth publishing, and the same is true of the history: an agent-authored commit history *is* the product's claim, so rewriting it to scrub a secret destroys the artifact in order to save it.

## What is actually true in the code today (recorded so the next reader is not misled)

- `publish.VerifyTree` inspects the committed tree at finalize and its result reaches `replication.json` (`redistribution.review`), which `publish.EligibleForPublic` reads. That chain is real. It sees the head tree only, not history.
- `publish.TranscriptLeaks` runs at finalize and **only prints a warning**. 005 specified that a leaking transcript bars the public flip; the manifest is committed before the transcript is rendered, and `ManifestFor`'s clean flag carries the tree result alone, so the eligibility decision cannot see it. That is a divergence from 005, not a design choice.
- `EligibleForPublic` and `github.SetPublic` have no caller outside tests. The flip is unwired — which is the containment described above, and also why the divergence has not yet cost anything.

## Candidate directions (none chosen)

1. **Make the secret worthless instead of absent.** The only credential the system deliberately puts in the sandbox is the mount token. Scope it to the one repository, give it a short life, and revoke it at finalize; then a token in the history is spent, not dangerous. This attacks the leak we cause, and it composes with everything below. It does nothing about a credential the agent found or was handed by a third party.
2. **Fail closed at the flip.** Fold `TranscriptLeaks` into the manifest and into `EligibleForPublic`, so both surfaces gate the one irreversible action. Cheap, restores 005's intent, and leaves the artifacts untouched. It does not make a flagged reproduction publishable — it just refuses honestly.
3. **Mask at render time, keep the raw event unpublished.** Publish a redacted `TRANSCRIPT.md`/`transcript.jsonl` and retain the unmasked stream internally. Preserves publishability; costs the losslessness that `raw` exists to guarantee, and a reader can no longer verify the evidence is complete.
4. **Republish from a verified tree.** On a leak, create a fresh repository from the cleared head tree and drop the history. Removes the secret, and removes the agent-authored history that is half the point. A last resort at best.

## Open questions

- What counts as a secret here — today it is a filename and high-signal content check, which is a backstop, not a proof. Widening it risks flagging the paper's own text and barring nearly every transcript, which 005 already rejected as self-defeating.
- Where masking belongs if it happens at all: the adapter (nothing unmasked ever enters the process), the renderer (published form only), or the gate (refuse, never launder).
- Whether a leak should ever be recoverable, or whether "private forever, with the reason recorded" is the right terminal state for a reproduction that leaked.
- History is not covered by anything today. Scanning every commit at finalize is possible; deciding what to do with a positive is the hard part, and it is question three again.

## Out of scope

Everything until the visibility flip is wired. When 006f's remaining pieces are taken, this spec is a prerequisite of the flip, not a follow-up to it: direction 2 is the smallest thing that has to be true before any reproduction can be made public.

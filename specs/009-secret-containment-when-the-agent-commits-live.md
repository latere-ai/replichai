---
title: "Secret containment when the agent commits live"
status: implemented
---

# 009 — Secret containment when the agent commits live

**Status:** commits 1–2 shipped and deployed; **commit 3 (mint + revoke + scrub) deferred** on a hard external constraint — see "The mount-token lifetime constraint" below. The visibility flip is wired (006f), so the structural containment that let this sit in the backlog is gone; the leak gate (commit 1) is the prerequisite of a safe publish and it is live. The App auth (commit 2) moves every *server-side* operation off the org PAT; the *mount* still carries the PAT, deliberately, until the constraint is resolved. The constraint is about a token the agent holds for a whole run; a local session has none, so [045l](045l-the-repository-comes-from-the-platform.md) **shipped commit 3 for the local path**, where the credential's whole life is one push: `MintRepoToken` and `RevokeToken` exist and the local publish uses them. The mount is unchanged.
**Scope:** what happens when a secret reaches a place we publish — the committed tree, the git history, or the transcript — given that the agent commits and pushes while it works. And the credential we ourselves put in the sandbox: make it worthless the moment the run ends.
**Depends on:** [002](002-publishing-reproductions.md) (the gate), [005](005-transcript.md) (the transcript), [006f](006f-taking-a-reproduction-out.md) (the mount and the public flip), [006i](006i-server-side-mount-and-publish.md) (the server-side finalize this hardens).

## Problem

The mount path (006f/006i) is the real publish path, and it removed the place where filtering used to happen. The agent commits and pushes into a live repository as it works; by the time the system sees anything, it is already pushed. Two kinds of secret can survive to a published surface:

- **a credential we issued** — the mount token, which authenticates the agent's push and therefore lives inside the sandbox. A hostile paper could prompt-inject to echo it, and it would land in the git history or the transcript.
- **a credential we did not issue** — one the agent downloaded inside a dataset, or that the paper's own code embeds. We never held this and cannot revoke it.

The two demand different answers, and the design uses both:

- **Make our own secret worthless instead of absent.** The mount token is scoped to one repository and short-lived; revoke it at finalize (or let its ≤1h life expire), and a copy in the history or transcript is *spent*. This is the primary control for the leak we cause. It requires the server credential to be able to *mint* such a token — which a PAT cannot do, and a GitHub App can.
- **Fail closed at the flip for a secret we cannot revoke.** Fold the transcript-leak scan into the manifest and the eligibility decision, so a foreign credential in the evidence layer bars the one irreversible action. Cheap, restores 005's intent, and leaves the artifacts untouched — a flagged reproduction stays private, it is not laundered.

## What is actually true in the code today (recorded so the next reader is not misled)

- `publish.VerifyTree` inspects the committed tree at finalize and its result reaches `replication.json` (`redistribution.review`), which `cmd/server` turns into `Reproduction.CanPublish` (`review == "clear"`), which the public flip reads (`handlers.go:clearedForPublish`). That chain is real, wired, and enforced. It sees the head tree only, not history.
- `publish.TranscriptLeaks` runs at finalize but its result **only prints a warning** (`cmd/replichai/main.go`) — nothing feeds it into `ManifestFor`'s clean flag, so a leaking transcript does **not** bar the public flip. 005 specified that it should. This is the divergence commit 1 closes.
- `EligibleForPublic`, `github.SetPublic`, and the members API **are wired and deployed** (006f): `POST /projects/{id}/public`, `GET/POST/DELETE /projects/{id}/members`, `GET /public`. (An earlier draft of this spec claimed they had no caller outside tests; that was made stale by 006f and is corrected here.)
- The mount carries the **server token** (`REPLICHAI_GITHUB_TOKEN`, a plain PAT sent as a Bearer) into the sandbox — `cmd/server/runner.go:mountFor` sets `Token: e.token`. So the credential inside the sandbox is org-scoped: a leak costs the organisation, not one repository. This is the blast radius commits 2–3 reduce.

## Decisions

**The server credential becomes a GitHub App, and the PAT is retired.** A PAT authenticates but cannot *mint* — GitHub offers no scoped sub-token of a PAT. The only credential that mints a temporary, single-repository, revocable token is a GitHub App (`POST /app/installations/{id}/access_tokens`, scoped by `repositories` + `permissions`, ≤1h life, revocable via `DELETE /installation/token`). So the App is not infrastructure layered on the PAT — it *replaces* it, and every server-side operation (create repo, invite, read head/tree, put file, flip visibility, enumerate the org for the registry) runs on an App installation token instead.

**Installation tokens expire, so the client refreshes.** An installation token lives ≤1h; the server lives for weeks. `github.Client` must therefore mint-and-cache the installation token behind its existing surface and refresh it on expiry (or a 401), so no caller changes and the server does not 401 an hour after each deploy. This is the one change that would break production if omitted, so it is part of the same commit as the App auth, never after it.

**The mount gets its own minted token, revoked at finalize.** For a run destined for the forge, the server (as the App) mints an installation token scoped to *exactly* that one repository with `contents: write`, and that is what enters the sandbox — never the App's broad token, never a PAT. At finalize the server revokes it. A token captured in the history or the transcript is then spent: it authenticates nothing.

**Our own token is scrubbed from the published transcript, not gated on.** The transcript-leak scanner matches secret patterns in event content; our minted token would match too, and after revocation it is harmless, so gating on it would falsely bar a clean reproduction. Instead, at finalize the server replaces the minted token's value in the event stream before both the leak scan and the render — it injected the token, so removing it costs no evidentiary value (it was never part of the agent's reasoning). On the resume path, where the process no longer holds the minted value, nothing is scrubbed and the scanner conservatively flags — fail-safe, and the finalize logs that it happened.

**A foreign secret in the transcript bars the flip, and the manifest says why.** `TranscriptLeaks` folds into the finalize's redistribution result: a tree leak yields `review: "excluded"` (third-party *data*, as today), and a transcript secret on an otherwise-clean tree yields a distinct `review: "secret_in_transcript"` — not `third_party_data: true`, which would misstate a credential as bulk data. `EligibleForPublic` already refuses any non-`clear` review, and `cmd/server` already derives `CanPublish` from `review == "clear"`, so both surfaces bar the flip with no further change. The reproduction is still committed intact and stays visible to the owner and collaborators; only the public flip is refused.

Rejected: **masking the published transcript while keeping the raw stream internal** — it preserves publishability at the cost of the losslessness `raw` exists to guarantee, and a reader can no longer verify the evidence is complete. Rejected: **rewriting history to scrub a foreign secret** — an agent-authored commit history is the product's claim; scrubbing it to save a reproduction destroys the artifact. A foreign leak is quarantined private with the reason recorded, never laundered.

## Interface

```go
// internal/github: the App authenticator, behind the existing Client surface.
// NewApp replaces New for the server; the Client mints and caches an installation
// access token and refreshes it on expiry, so CreateRepo/Invite/HeadCommit/
// TreePaths/PutFile/SetPublic/ListRepos are unchanged for their callers.
func NewApp(appID int64, privateKeyPEM []byte, owner string, opts ...Option) (*Client, error)

// MintMountToken mints the fine-grained, single-repository, ≤1h token the mount
// carries — the only credential that ever enters the sandbox.
func (c *Client) MintMountToken(ctx context.Context, owner, name string) (token string, err error)

// RevokeToken spends a minted token immediately (DELETE /installation/token,
// authenticated with the token itself). Idempotent: an already-expired token is
// not an error.
func (c *Client) RevokeToken(ctx context.Context, token string) error
```

The `MintMountToken`/`RevokeToken`/`NewApp`-minting seam above is the design of record; `NewApp` and the refreshing installation-token client shipped in commit 2. The mint and the revoke now exist as `github.MintRepoToken`/`github.RevokeToken` ([045l](045l-the-repository-comes-from-the-platform.md)) and are used by the local publish path; the **mount** still carries the PAT, so `MintMountToken` in the mount's sense stays deferred with commit 3. Likewise the `FinalizeLive` `mountToken` parameter below is the deferred shape — commit 1's `FinalizeLive` keeps its current signature and grows the scrub parameter only when minting lands.

```go
// internal/publish: the leak scan folds into the redistribution result, with a
// distinct review reason (shipped, commit 1), and — deferred with commit 3 — the
// minted token is scrubbed before scan+render.
const ReviewSecretInTranscript = "secret_in_transcript"

// ManifestFor takes the classified redistribution result rather than a lone
// clean bool, so the manifest records *why* a tree is barred.
func ManifestFor(id paper.Identity, repo, methodDigest string, verdict provider.Grade, created time.Time, redist Redistribution) Manifest

// FinalizeLive gains the minted-token value to scrub (empty on the resume path).
func FinalizeLive(ctx, forge, owner, name string, id paper.Identity, methodDigest string, verdict provider.Grade, events []provider.Event, mountToken string, now func() time.Time) (FinalizeReport, error)
```

## The three commits

Ordered so production is never broken, and the PAT keeps working until the App is proven. `REPLICHAI_GITHUB_TOKEN` stays in the config secret through the whole sequence; it is retired only after commit 2 is verified in prod, so a rollback is one `kubectl set image`.

**Commit 1 — fail closed on a leaking transcript.** Pure `internal/publish`; no credential or infra change; ships and deploys on its own. `FinalizeLive` computes `TranscriptLeaks` before the manifest and folds it into the redistribution result; `ManifestFor` takes the classified `Redistribution`; a transcript secret yields `review: "secret_in_transcript"`. This is the prerequisite 005 asked for and the smallest safe thing.

**Commit 2 — the App client with token refresh.** `internal/github` gains `NewApp`, JWT (RS256, hand-rolled to match the package's net/http ethos — no JWT dependency is in the tree), installation-token mint+cache+refresh, all behind the current `Client` surface. `cmd/server` constructs the App when `REPLICHAI_GITHUB_APP_ID`/`REPLICHAI_GITHUB_APP_KEY` are set and falls back to the PAT otherwise, so the deploy is safe before the App credentials exist. `ListRepos` (the registry's org enumeration) is verified against the App before cutover.

**Commit 3 — mint, mount, revoke, scrub. DEFERRED.** `MintMountToken`/`RevokeToken`; `runner.go:mountFor` would carry the minted token; the server would revoke at finalize and pass the minted value to `FinalizeLive` to scrub. Blocked by the constraint below, and correct only as a unit: the scrub is safe *only* on a token that is already spent (revoked). Scrubbing the current mount credential — a live, org-scoped PAT — out of the transcript would hide a real, still-working leak that commit 1's gate exists to catch. So this commit is not partially shippable as "scrub only"; it waits for the mint+revoke it depends on.

## The mount-token lifetime constraint

The mount sets its credential *once*, at session creation: the anthropic adapter maps `RepoMount.Token` to a `github_repository` resource's `AuthorizationToken` (`internal/provider/anthropic/anthropic.go`), and the agent pushes with that one token for the whole session. There is no refresh path in the mount. A GitHub App installation token lives **at most one hour** and cannot be extended. Audits run close to and sometimes past that: the first real 006i run took 53 minutes. So a minted mount token would silently lose push access mid-run for any audit exceeding its life — the agent's later commits would fail, and the reproduction would be truncated with no clear signal.

The org PAT does not expire, which is exactly why the mount still carries it. Minting a per-repo token is the right hardening, but only once one of these is true:

- audits are bounded well under an hour (a run deadline shorter than the token life, so a run can never outlive its credential), or
- the mount can refresh its token mid-session (a backend capability that does not exist today).

Until then the mount keeps the non-expiring PAT. The blast radius is real (a hostile paper could exfiltrate an org-scoped token) but bounded by the closed beta: only invited users submit, and repositories start private. This is the same structural containment 006f relied on, narrowed now to the mount alone — commits 1 and 2 removed it everywhere else.

## Acceptance criteria

1. A finalize whose transcript carries a secret writes `redistribution.review == "secret_in_transcript"` (and `third_party_data == false`), yields `CanPublish == false`, and the public flip refuses it `409` — asserted end to end; a clean transcript is unaffected. (Commit 1)
2. A tree leak still yields `review == "excluded"` and `third_party_data == true`; the two reasons never collide. (Commit 1)
3. The App client mints, caches, and refreshes an installation token: a unit test against a fake GitHub App server asserts a second call after the token's expiry re-mints rather than reusing the stale token, and that `CreateRepo`/`Invite`/`HeadCommit`/`ListRepos` carry the installation token, never the App JWT. (Commit 2)
4. With no App configured, the client falls back to the PAT and every server operation behaves exactly as before — asserted, so the deploy is safe pre-App. (Commit 2)
5. *(Deferred with commit 3.)* `MintMountToken` returns a token scoped to the one repository with `contents: write` and nothing else — asserted against the fake App server's recorded request body (`repositories`, `permissions`).
6. *(Deferred with commit 3.)* The mount carries the minted token, never the App's broad token and never the PAT — a test asserts the value placed in `RepoMount.Token` is the mint result and differs from the server credential.
7. *(Deferred with commit 3.)* `RevokeToken` is called at finalize and is idempotent on an already-expired token; the minted value is scrubbed from the committed transcript and from the leak scan, so a run that echoes its own mount token still verifies clean. On the resume path (minted value absent) the scan conservatively flags and the finalize logs it.

## Out of scope

History scanning beyond the head tree (a secret in an earlier commit that a later commit deletes) — flagged as a known gap, not closed here; the mount-token revocation makes the most likely such secret worthless regardless. Widening what counts as a secret beyond the current filename + high-signal-content backstop — 005 already rejected a broad content scan as self-defeating (it would flag the paper's own text). Multi-tenant credential isolation beyond the single `open-hai` installation.

# 006f — Taking a reproduction out

**Status:** the mount publish, verifier, and forge invite; the **owner public flip with its recorded sign-off, the optional forge repo flip (with a per-project opt-out), the public zone, and the per-project members API** (built and deployed); and the **two-tier model** in code and data (the trusted tier removed). The members API reconciles the membership store's authority to 006g's *owner* by seeding the owner as the project's lead (idempotent bridge), so ownership is the single authority. The mount carries the server token (`REPLICHAI_GITHUB_TOKEN`), as the CLI publish has always done. The **server-side backend mount-publish is now wired** ([006i](006i-server-side-mount-and-publish.md)): the live server creates the repo up front, mounts it so the agent commits live, and finalizes via shared `publish.FinalizeLive` — it no longer depends on CLI-produced repos. The **two-token mount split** below (a repo-scoped GitHub App token in the sandbox, replacing the org token) is the remaining hardening and is now being built under active spec [009](009-secret-containment-when-the-agent-commits-live.md), whose first commit (failing the public flip closed on a leaking transcript) has shipped.
**Superseded in part:** the one-way rule below (Decisions §"Public is an owner action…", acceptance criterion 6) is ended by [006o](006o-withdrawing-a-publication.md): an owner may withdraw a publication back to private, and the forge repository follows. Everything else here stands — the gate, the sign-off, the owner-only check, and the record, which a withdrawal retracts rather than erases.
**Scope:** publishing a project's reproduction as a real GitHub repository with agent-authored commit history, inviting collaborators to the project, and — for the owner, with a signed responsibility attestation — making the project (and optionally its repository) public. A child of [006](006-web-application.md).
**Depends on:** [002](002-publishing-reproductions.md) (the redistribution gate, reused as a verifier; the manifest), [004](004-go-port.md) (the provider seam it extends), [006g](006g-projects-and-replication-sessions.md) (the project that owns visibility and the membership it invites into). Consumes [006c](006c-identity-and-access.md) (the linked GitHub login, the membership store).

## Problem

Spec 002 publishes by having a *publisher* assemble the cleared files and push one commit. That works, and it stays the offline and CLI path. But it throws away the thing a reproduction most wants to show — the *process* — and it stops at a local forge: it never reaches GitHub, never grants the repo to the person who asked for it, and never makes anything public.

This spec does all of that, on the mechanism a live spike proved rather than a hoped-for one.

## What the spike established

A throwaway run (2026-07-25) confirmed the mechanic this depends on: the managed-agents backend mounts a GitHub repository into the agent's sandbox — `BetaSessionNewParams.Resources`, a `github_repository` resource carrying the repo URL, an `AuthorizationToken`, and a mount path — and **the agent commits and pushes its progress live**. An agent told only to "write a file, commit, push" landed commit `1e76be1` on a real private repo under `open-hai`. So the sandbox has network to push, the mount is writable, and the token authenticates the push. The published repository can carry agent-authored history because the agent authors it in place.

## Decisions

**The repository is created first and mounted, not pushed at the end.** The flow inverts 002's:

1. Create an empty, private repository under the organisation.
2. Mount it into the audit's session; the agent commits its reproduction as it works.
3. After the run: verify what was committed, read the head commit, write the manifest, invite the user.

002's `FSForge`/`Forge` push path is not removed — it remains the offline and CLI path, and the fallback when a run is not destined for GitHub. This spec is the *backend* path, and it revises how a to-be-published audit runs.

**Two tokens, two blast radii.** The mount token lives *inside the sandbox*, where a hostile paper could try prompt-injection to reach it. So the credential is split:

| Token | Holds | Used for | In the sandbox? |
|---|---|---|---|
| Server token (org admin / GitHub App) | the deployment | create the repo, send the invitation, read head, flip visibility | **never** |
| Mount token (fine-grained, one repo, short-lived) | the run | the agent's clone and push | yes — and nothing else |

The mount token grants write to exactly the one repository it belongs to. A leak costs that repo, not the organisation.

**The mounted repository is a bounded workspace.** Because the agent pushes live, the publisher can no longer pre-filter — so the boundary moves into the method. The worker is instructed to commit only its reproduction (the reports and `src/`) into the mounted repo, and to keep every download (paper PDF, benchmark, corpus) in a scratch directory *outside* the mount. Third-party data then physically cannot be committed, and live push is safe by construction. This is a method change, and it is the primary control.

**The gate becomes a post-run verifier — and it must invert.** The publisher can no longer filter before a push that has already happened; it *verifies* the committed tree afterwards. But 002's allow-list (worker = the four report files and `src/`) is wrong here: an agent-authored repository is a real software project — `.gitignore`, `LICENSE`, `requirements.txt`, tests, configs, generated results — not a fixed set of report files, so an allow-list flags all of it. So the method is the primary control (keep downloads out of the repository), and `publish.VerifyTree` is the backstop: it flags only what is almost certainly third-party — the paper PDF, a dataset, model weights, an archive, a database, or a leaked secret. A tree that verifies clean gets `redistribution: clear` and is eligible for the visibility flip; a tree that flags anything is kept private and **barred from ever being made public** — the fail-closed posture, enforced after the fact.

**Ownership by invitation.** The user is added as a collaborator (`PUT /repos/{owner}/{repo}/collaborators/{username}`), which for a non-member creates an invitation they accept. The username is the *proven* GitHub login from 006c's link step, never a typed one. No user ever grants the service `repo` scope; the organisation invites outward.

**Collaboration is per-project, driven by the owner, and admits nobody to the platform.** A project owner (006g) invites a collaborator *by email* into the project — a row in the membership store (006c), bound one-way on the collaborator's first login. This is the app-level invitation; the forge collaborator invite above is what follows once that person has a *proven* GitHub login and a session's repository to be added to. The two are distinct on purpose: project membership decides who may *see and operate the project in the app*; the forge invite decides who may *push to the repository*. Adding a collaborator by email never grants a platform tier: membership and tier are resolved independently, and neither can be satisfied by the other (006c's orthogonality rule). What that *cost* an invitee changed with [006q](006q-sign-in-is-open-budget-is-not.md) — signing in now self-grants `TierUser`, so an invitee reaches the app on their own rather than answering `not_granted` until an admin acts. The rule stands; the wall it used to put in front of a collaborator does not. The membership store already exists (006c); this spec gives it its first HTTP surface: `GET/POST/DELETE /projects/{id}/members`, owner-only.

**Public is an owner action, gated by a responsibility sign-off, one-way and recorded — at the project.** Visibility lives on the *project* (006g), not on a loose repository, and the *owner* decides. Making a project public is never automatic and never a bare toggle:

- It refuses any project whose reproduction manifest does not show it passed 002's gate (`redistribution.review == "clear"`) — a flagged tree can never be made public, the fail-closed posture enforced after the fact.
- It requires an **explicit responsibility sign-off**: the owner affirms, in a recorded attestation, that they are responsible for publishing these results. The signed attestation's text version, the deciding principal, and the timestamp are written to Postgres (`signed_off_by`, `signed_off_at`, `attestation_version`) as the app's audit trail.
- Only then does it mark the project `public` and, *optionally*, flip the repository public.

**The repository flip is optional; the project's artifacts are public either way.** Because every project's artifacts are also served by the app's own read-only pass-through (the file tree and download in [006](006-web-application.md), OSF-style), a project can go public without ever exposing — or even revealing — an underlying GitHub repository. Two modes fall out of [006](006-web-application.md)'s storage decision:

- **No GitHub connection.** The user never sees a repo; the app serves the artifact file tree and a full download, and going public opens exactly those artifacts. The repo under `open-hai` stays a private implementation detail.
- **GitHub-connected.** The user operates the repository externally (collaborator access, clone, push), and on going public may **opt the repository out** of the public flip — the in-app artifacts still go public, but `SetPublic` is not called, so the repo itself stays private.

(Where those artifact files physically live — the `open-hai` repo today, a cheaper S3-backed git store later — is [006](006-web-application.md)'s `Store` seam and [008](008-drive-data-plane.md)'s drive integration, deferred and out of scope here.)

Rejected: a trusted-tier that lets any trusted user publish anyone's project — it separates the decision from the person responsible for it. Rejected: flipping the repo without recording a sign-off — publishing results carries responsibility, and the record is what makes that responsibility real rather than implied.

## Interface

The provider seam gains a neutral, optional repository mount — no backend named above the adapter, as [004](004-go-port.md) requires.

```go
// In internal/provider: a version-control repository to mount into the run's
// sandbox so the worker commits into it directly. Host-agnostic; the adapter
// maps it to its backend's resource (the anthropic adapter → a github_repository
// session resource). The zero value mounts nothing, so audits are unchanged.
type RepoMount struct {
	URL       string // clone URL of an existing repository
	Token     string // write credential; lives only in the sandbox
	MountPath string // where in the sandbox; empty means the backend default
}
// RunSpec gains: Mount RepoMount

// In internal/github (hand-rolled net/http, one small client): the server-side
// operations, none of which enter the sandbox.
type Client struct{ /* server token */ }

func (c *Client) CreateRepo(ctx, owner, name string, private bool) (Repo, error)
func (c *Client) Invite(ctx, owner, name, login string) (Invitation, error)
func (c *Client) HeadCommit(ctx, owner, name, branch string) (string, error)
func (c *Client) SetPublic(ctx, owner, name string) error

// MintMountToken produces the fine-grained, single-repo, short-lived token the
// mount carries (a GitHub App installation token scoped to the one repo).
func (c *Client) MintMountToken(ctx, owner, name string) (string, error)
```

The public flip is an owner action recorded on the project (006g), so the project store gains the sign-off write, and the membership store (006c) gains its owner-only HTTP surface:

```go
// On the project store (006g): the one-way public flip with its recorded sign-off.
// Refuses a project whose manifest is not `clear`; writes signed_off_by / signed_off_at /
// attestation_version and marks the project public; then flips the repo (SetPublic).
SetPublic(ctx, owner Principal, projectID, attestationVersion string) error

// Owner-only project membership routes (over the existing access.Membership store):
//   GET    /projects/{id}/members            list contributors
//   POST   /projects/{id}/members {email,role}   invite by email (binds one-way on first login)
//   DELETE /projects/{id}/members/{email}    remove
// And the public flip:
//   POST   /projects/{id}/public {attestation_version}   owner + sign-off; 403 if not owner,
//                                                          409 if manifest not clear
// And the way back out (006o):
//   POST   /projects/{id}/private {reason}   owner; withdraws the publication and
//                                              closes the repositories. Keeps the sign-off.
```

**The public zone shows the initiator.** A project made public joins a **public zone** — a read surface every signed-in user can browse — that lists public projects with their owner (the initiator who triggered the replication), the paper, and the verdict, and opens the full public project: its sessions, transcripts (006h), datasets, and artifacts. There is no org, so the public zone *is* the shared visibility — individuals and their public replications, nothing between.

The manifest from 002 is reused unchanged. The redistribution *check* is not: `publish.VerifyTree` flags committed third-party data (by a blocklist of paper/dataset/weights/archive/database extensions, plus secret names), where 002's `PlanPublication` allow-listed a fixed output set. Different shape of input — a full repository, not a method's fixed output — demands a different rule.

## Acceptance criteria

1. The adapter mounts a repository when `RunSpec.Mount` is set and audits identically when it is not — asserted against the memory adapter (mount ignored) and by a unit test of the anthropic adapter's session params carrying the `github_repository` resource.
2. The two tokens never cross: a test asserts the server token is never placed in a `RepoMount`, and the mount token grants only the one repository.
3. The bounded-workspace method keeps third-party data out of the repo — verified by an evaluation case whose reproduction repository contains only worker-written paths.
4. `VerifyTree` flags committed third-party data (a paper PDF, dataset, weights, archive, database, or secret) but not a normal repository's own files (`.gitignore`, deps, tests, results); a flagged tree yields `redistribution.review != "clear"` and is refused for the public flip. A test asserts a real reproduction repo verifies clean.
5. Inviting a user adds them as a collaborator and requests no `repo` scope from them — asserted against a fake GitHub server.
6. The public flip is **owner-only**, requires the recorded **sign-off** (writes `signed_off_by`/`signed_off_at`/`attestation_version`), refuses a project whose manifest is not `clear` (`409`), and is one-way — asserted directly, including that a non-owner is refused `403` and an un-signed request changes nothing.
7. The per-project members API is owner-only and admits nobody to the platform: a test has an owner invite a contributor by email, asserts the contributor can access that private project and that membership alone conferred no tier. *(Since [006q](006q-sign-in-is-open-budget-is-not.md) their own sign-in grants one, so the original form of this test — refused on every operator route — no longer describes the product.)*
8. A project made public appears in the public zone to a different signed-in user, showing its owner/initiator, and opens the full project (sessions, transcripts, artifacts); a private project never appears there.

## Out of scope

The submit/track API (006a) and the project/session entity (006g), the identity gate (006c), the transcript viewer itself (006h), and the frontend rendering (006e) — this spec makes the flip, the sign-off record, the members API, and the public-zone data; those specs render and mount them. Multi-tenancy beyond individual users and the public zone (no orgs). Rewriting history to *filter* a repository that committed third-party data — a violation is flagged and quarantined from publication, not laundered.

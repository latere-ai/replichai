# 006f — Taking a reproduction out

**Status:** draft
**Scope:** publishing a reproduction as a real GitHub repository with agent-authored commit history, granting it to a user by invitation, and — for trusted users — making it public. A child of [006](006-web-application.md).
**Depends on:** [002](002-publishing-reproductions.md) (the redistribution gate, reused as a verifier; the manifest), [004](004-go-port.md) (the provider seam it extends). Consumes [006c](006-web-application.md) (the linked GitHub login, the trusted tier).

## Problem

Spec 002 publishes by having a *publisher* assemble the cleared files and push one commit. That works, and it stays the offline and CLI path. But it throws away the thing a reproduction most wants to show — the *process* — and it stops at a local forge: it never reaches GitHub, never grants the repo to the person who asked for it, and never makes anything public.

This spec does all of that, on the mechanism a live spike proved rather than a hoped-for one.

## What the spike established

A throwaway run (2026-07-25) confirmed the load-bearing mechanic: the managed-agents backend mounts a GitHub repository into the agent's sandbox — `BetaSessionNewParams.Resources`, a `github_repository` resource carrying the repo URL, an `AuthorizationToken`, and a mount path — and **the agent commits and pushes its progress live**. An agent told only to "write a file, commit, push" landed commit `1e76be1` on a real private repo under `open-hai`. So the sandbox has network to push, the mount is writable, and the token authenticates the push. The published repository can carry agent-authored history because the agent authors it in place.

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

**Public is a trusted-tier, gated, one-way, recorded action.** Making a reproduction public is never automatic. It is available only to admin-set *trusted* users (006c); it refuses any repository whose manifest does not show it passed 002's gate (`redistribution.review == "clear"`); it is treated as irreversible; and it records the deciding principal. Trust, not ownership: any trusted user may publish any reproduction.

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

The manifest from 002 is reused unchanged. The redistribution *check* is not: `publish.VerifyTree` flags committed third-party data (by a blocklist of paper/dataset/weights/archive/database extensions, plus secret names), where 002's `PlanPublication` allow-listed a fixed output set. Different shape of input — a full repository, not a method's fixed output — demands a different rule.

## Acceptance criteria

1. The adapter mounts a repository when `RunSpec.Mount` is set and audits identically when it is not — asserted against the memory adapter (mount ignored) and by a unit test of the anthropic adapter's session params carrying the `github_repository` resource.
2. The two tokens never cross: a test asserts the server token is never placed in a `RepoMount`, and the mount token grants only the one repository.
3. The bounded-workspace method keeps third-party data out of the repo — verified by an evaluation case whose reproduction repository contains only worker-written paths.
4. `VerifyTree` flags committed third-party data (a paper PDF, dataset, weights, archive, database, or secret) but not a normal repository's own files (`.gitignore`, deps, tests, results); a flagged tree yields `redistribution.review != "clear"` and is refused for the public flip. A test asserts a real reproduction repo verifies clean.
5. Inviting a user adds them as a collaborator and requests no `repo` scope from them — asserted against a fake GitHub server.
6. `SetPublic` is trusted-only, refuses a repository whose manifest is not `clear`, and records the deciding principal — asserted directly.

## Out of scope

The web surfaces that drive this — the submit/track API (006a), the identity and trusted-tier gate (006c), the frontend (006e). Multi-tenancy. Rewriting history to *filter* a repository that committed third-party data — a violation is flagged and quarantined from publication, not laundered.

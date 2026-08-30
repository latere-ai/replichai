---
title: "The repository comes from the platform, not from your laptop"
status: built
---

# 045l — The repository comes from the platform, not from your laptop

**Status:** built.

**Scope:** how a session run on somebody's machine gets a GitHub repository and the credential to push it, without that person holding an organisation credential. Closes [009](009-secret-containment-when-the-agent-commits-live.md)'s deferred commit 3 for the local path only; the mounted path keeps the PAT and the constraint that deferred it stands.

**Depends on:** [045g](045g-the-host-pushes.md), [045j](045j-a-bearer-the-api-accepts.md), [045k](045k-a-local-session-reaches-the-cloud.md), [009](009-secret-containment-when-the-agent-commits-live.md), [006f](006f-taking-a-reproduction-out.md), [003](003-the-registry-of-reproductions.md).

## Problem

`replichai audit <paper> --publish --runtime local` needs `REPLICHAI_GITHUB_TOKEN` on the machine that runs it. That variable holds an organisation-scoped personal access token: it can create, read, write and delete every repository under `open-hai`, and it does not expire.

So the local path asks each operator to keep the broadest credential in the system on a laptop, in a shell profile, for the length of their employment. The hosted path asks nobody for anything — the server holds the credential and the operator never sees it.

The asymmetry is not a property of running locally. It is a property of who creates the repository. The server already creates one for every session it runs (`githubForge.Prepare`); it simply has no way to do that for a session it is not running.

## Why this can ship now, when 009 could not

[009](009-secret-containment-when-the-agent-commits-live.md) decided the right credential is an App-minted token scoped to one repository, and then deferred it on a hard constraint:

> The mount sets its credential *once*, at session creation … A GitHub App installation token lives **at most one hour** and cannot be extended. Audits run close to and sometimes past that: the first real 006i run took 53 minutes.

That constraint is about a token the *agent* holds for the *whole run*. The local path does not have one. [045g](045g-the-host-pushes.md) already moved the push out of the session:

```
  hosted:   mint ──┬──────────── 53 min of agent commits ─────────────┬── push
                   └──── token must stay valid this whole time ───────┘
                                     ⟹ 1h life is a real risk

  local:    ┌───────── 53 min of agent commits, no remote ─────────┐
            └── nothing to authenticate ──┘   then:  mint ── push ──┴── revoke
                                                     └── seconds ──┘
```

009 named two conditions that would unblock minting; the first was "audits are bounded well under an hour, so a run can never outlive its credential." The local path satisfies it — not by shortening audits, but because the credential's whole life is the push, and the push is a `git push` of one small repository.

Let `T` be the token's life and `P` the push's duration. The hosted path needs `T > audit + push` and cannot guarantee it. The local path needs only `T > P`, where `P` is seconds and `T` is an hour.

## Decisions

**The server allocates the name and creates the repository, using the code the hosted path uses.** `githubForge.Prepare` calls `registry.AllocateName` then `CreateRepo`, and a second implementation would let a local session and a hosted session decide the same slug for the same paper. The route calls the same seam.

**Provisioning is its own route, not a field on import.** The order inverts: today the CLI creates the repository, pushes, then imports a record naming it. With the server provisioning, the CLI must hold the credential *before* it has anything to import — a session that has not finished cannot be imported, and a session that has finished has nowhere to push. So:

```
  POST /projects/{id}/repository     →  { repo, url, token, expires_at }
       ↓                                          (the CLI pushes)
  POST /projects/{id}/sessions/import ←  { …, repo }
```

The route is on the project, not on a session: the repository is allocated from the paper's identity, and the session that fills it does not exist yet.

**A deployment with no App refuses.** `newForgeClient` falls back to a PAT when `REPLICHAI_GITHUB_APP_ID`/`_KEY` are unset, and a PAT cannot mint a sub-token — GitHub offers no such thing. Handing the organisation PAT back over HTTP would be strictly worse than the variable it replaces, because it would put the same credential on the same laptop *and* in a response body. So the route answers `501 minting_unavailable`, and the CLI says which variable to set and falls back to `REPLICHAI_GITHUB_TOKEN` if it is present.

**The CLI revokes the token when the push is done.** The token expires within the hour regardless, but expiry is not a completion signal and the push is over in seconds. The holder revokes what it holds: `DELETE /installation/token` is authenticated by the token itself, so revocation needs no further credential and cannot be used against anybody else's.

**The token carries `contents: write` on one repository and nothing else.** Not `administration`, not `metadata: write`, not a second repository. A leaked minted token can push to the reproduction it was minted for, for the rest of its hour, and do nothing else anywhere.

## The shape

```go
// internal/github: the mint that 009 designed and never exposed.
//
// mintLocked already takes a scope and every caller passes nil. This is the
// scope.
func (c *Client) MintRepoToken(ctx context.Context, owner, name string) (Token, error)
func (c *Client) RevokeToken(ctx context.Context, token string) error

// Token is a credential with an end.
type Token struct {
    Value   string
    Expires time.Time
}
```

```go
// internal/httpapi: what a terminal asks for before it pushes.
type RepoProvisioner interface {
    // Provision allocates a name for the paper, creates the private
    // repository, and mints a credential that can write to it and to
    // nothing else.
    Provision(ctx context.Context, owner string, id paper.Identity) (Provisioned, error)
}

type Provisioned struct {
    Repo    string    // owner/name
    URL     string    // the clone URL to push to
    Token   string    // scoped, short-lived
    Expires time.Time
}
```

The handler is a project-membership check, a paper lookup, and a call. It does not touch the job store: nothing has run yet.

## What the terminal does

```
replichai audit <paper> --publish --runtime local
  │
  ├─ signed in?  ── no ──▶ refuse: run `replichai login`
  │
  ├─ POST /projects/{id}/repository
  │     ├─ 201 ──▶ push with the minted token, then DELETE /installation/token
  │     └─ 501 ──▶ say which variable the deployment is missing;
  │                fall back to REPLICHAI_GITHUB_TOKEN if it is set,
  │                otherwise refuse and name both ways forward
  │
  └─ POST /projects/{id}/sessions/import   { …, repo }
```

`--publish` without `--project` keeps today's behaviour: there is no project to provision against, so the environment variable is the only path and the CLI says so.

## Acceptance criteria

1. `MintRepoToken` sends `repositories: ["<name>"]` and `permissions: {"contents": "write"}` and no other key — asserted against the fake App server's recorded request body, so a widening of the scope fails the test rather than the audit log.
2. The minted value differs from the server's own installation token and from the PAT: a test asserts the value handed to the terminal is the mint result.
3. `RevokeToken` authenticates with the token being revoked, and is idempotent on one already expired.
4. A deployment with no App answers `501 minting_unavailable`; the CLI prints which variables the deployment needs and falls back to `REPLICHAI_GITHUB_TOKEN` when it is set. A PAT is never returned in a response body — asserted by a test that fails if the handler ever writes the server credential.
5. A member of the project may provision; a non-member gets the same `403` every other project write gets.
6. The chain test ([045k](045k-a-local-session-reaches-the-cloud.md)'s successor in `cmd/replichai`) provisions through the route against a fake App server, pushes to the repository the server named, and imports the record — so the mint request is asserted on the path an operator actually walks.

## Out of scope

The mounted path. It sets its credential once at session creation and the agent holds it for the whole run, so 009's lifetime constraint stands there unchanged and the mount keeps the PAT.

Per-operator installations. One `open-hai` installation serves every operator; who may provision is decided by project membership, not by a forge identity.

Revocation on a crash. A terminal killed between the mint and the revoke leaves a token that expires within the hour. That is the bound the design already accepts, and adding a server-side sweep would need the server to hold what it deliberately handed away.

## Outcome

Built. `replichai audit <paper> --publish --project <id> --runtime local` runs a
local session against a repository the platform allocated, pushes with a
credential scoped to that repository, and revokes it when the finalize is done.
`--publish` without `--project` keeps `REPLICHAI_GITHUB_TOKEN`, and a deployment
with no App falls back to it with the missing settings named.

Four things diverged from the design above.

**Filing the paper is part of asking for its repository.** The route refuses a
paper the project does not hold, and a project created a moment ago holds none,
so provisioning would have 404'd for every fresh project. The terminal files the
paper first, using the idempotent `AddPaper` that `push` already uses. This was
found by running the chain test, not by reading the design.

**The lookup key is derived offline, not from the resolved identity.**
`paper.Resolve` answers `10.1145/…` where the paper source is unreachable and
`doi:10.1145/…` where it is not, while the app always files under the offline
`paper.Key`. Asking with the resolved key made the lookup depend on a paper
source being up. The terminal now derives it the way the app does.

**`--invite` is refused on this path.** A minted credential carries
`contents:write`; adding a collaborator is an `administration` call. Refusing
before anything is created beats discovering it after the reproduction has been
pushed. Inviting through the platform, which holds a credential that can, is not
built.

**`CanMint` was added.** The composition root has to know whether the forge
credential can mint before it wires the provisioner, and the only other way to
find out would have been to mint a token nobody asked for.

**The real forge is unexercised.** Every test here runs against a fake: the
route against a fake provisioner, the chain against a bare repository on disk,
the mint against a fake GitHub App. What has not run is `forgeProvisioner`
against a real installation — that `Prepare` and `MintRepoToken` compose, that
GitHub grants `contents:write` on a repository created seconds earlier, and that
a push authenticated by an installation token is accepted. The push half is the
least uncertain of the three: `LocalPush.remoteWithUser` already defaults the
user part to `x-access-token`, which is what an installation token needs. One
`--publish --project` run against the real org would close all three.

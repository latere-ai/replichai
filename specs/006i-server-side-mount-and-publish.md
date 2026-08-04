# 006i — Server-side mount and publish

**Status:** built (pending prod verification of a full web-submitted run). `engineRunner` now walks the live-mount flow: Resolve creates the private repo and records the paper identity (durable `RepoName`); Execute mounts it so the agent commits live; Publish finalizes through the shared `publish.FinalizeLive` (manifest + committed transcript). The CLI's finalize was extracted into that shared helper behind a `LiveForge` seam, so the two paths cannot drift. The forge sits behind a small runner-side interface, so the phase logic is covered with no network, and the `RepoName` phase handoff is covered end to end through the worker — including a restart-resume. A status-read failure at publish fails the phase (retries) rather than writing a sticky `unknown` manifest.

Before this, the server audit path walked the phases but never mounted a repository nor published, so a web-submitted session graded but produced no repository — its Artifacts, Verdict, and committed-transcript tabs were empty and its work evaporated with the sandbox.
**Scope:** the *backend* half of [006f](006f-taking-a-reproduction-out.md). It changes how `engineRunner` runs the three audit phases (resolve, run, publish); it adds no new HTTP surface, no new store, and no new user-facing concept. A child of [006f](006f-taking-a-reproduction-out.md).
**Depends on:** [006f](006f-taking-a-reproduction-out.md) (the mount publish, the verifier, the manifest — the mechanism this wires server-side), [006g](006g-projects-and-replication-sessions.md) (the job that carries `repo_name`), [003](003-reproduction-registry.md) (name allocation), [004](004-go-port.md) (the provider seam and `RepoMount`).

## Problem

`replichai audit --publish` (the CLI) creates a private repo, mounts it into the sandbox, lets the agent commit live, then finalizes — verify the committed tree, write `replication.json`, commit the transcript. The web backend runs the *same* engine through `engineRunner`, but that runner:

- `Execute` calls `auditor.Run(ref, workDir)` with **no** `audit.WithMount(...)` — nothing is mounted, so the agent has no repository home;
- `Publish` is a **pass-through stub** — nothing is verified, no manifest, no transcript commit;
- `repo_name` is therefore never set.

So a web-submitted reproduction runs, grades, and disappears. Only its managed-agents session survives (the live transcript, 006h), which is why the session's Artifacts/Verdict tabs are empty and `repo_name` is blank. This is the drift 006f predicted: "the server-side backend mount-publish is not wired; the live server publishes via the CLI-produced repos."

## What the credential check established

006f split the mount credential into two tokens on the assumption that "the mount token lives *inside the sandbox*, where a hostile paper could try prompt-injection to reach it." A docs check (2026-07-27) refines that: Anthropic's managed-agents platform holds a `github_repository` resource's `authorization_token` at its **egress layer** — the vaults model states secrets are stored as opaque placeholders and substituted at egress, so "the agent never sees the secret value," and the GitHub resource token "is not echoed in API responses." The raw token is therefore **not readable inside the sandbox**.

Consequence for this spec: a **single server token** (`REPLICHAI_GITHUB_TOKEN`, as the CLI has always used) is sufficient for credential secrecy in the current trusted-submission phase. The two-token split remains a defense-in-depth for when submissions open to untrusted papers — a blast-radius reduction against a compromised *platform egress*, not a fix for a token exposed in the sandbox — and stays out of scope here.

## Decisions

**Mirror the CLI flow across the three phases; the mount is the boundary.** No new model — the phases already exist; they gain the work they were missing:

| Phase | Added work |
|---|---|
| **Resolve** | After `paper.Resolve`, allocate a collision-free name through the registry, create the private repository, record the resolved identity into it (`.replichai/paper.json`), and **set `job.RepoName`**. The worker persists that at the transition into `running` (`apply` copies `RepoName`), so it is durable before the run mounts it and survives a restart. |
| **Run** | Reconstruct `provider.RepoMount{URL, Token, MountPath}` from the persisted `RepoName` + the server token, and pass `audit.WithMount(mount)`. The agent commits its reproduction live to `/workspace/repro`; third-party downloads stay outside it (the mount protocol the auditor already injects). |
| **Publish** | Read the recorded identity back from the repo (not the rate-limited paper source), fetch the run's events via the provider transcript, and finalize: verify the committed tree, write `replication.json`, commit the transcript. Set nothing else — `RepoName` is already durable. |

**The mount is the redistribution boundary — no new classifier.** Because the agent commits only into the mounted repository and keeps downloads outside it, "what to publish" is answered by "what the agent committed," exactly as in the CLI. The Files-API alternative (run unmounted, pull every session file, filter) was rejected: it would republish third-party data unless a classifier caught every dataset, trading a proven boundary for a heuristic.

**Extract the CLI's finalize into a shared `publish.FinalizeLive`; keep repo-prep inline.** The finalize logic (`finalizePublication` + its manifest/transcript helpers) moves into `internal/publish` behind a `LiveForge` seam that `github.Client` already satisfies, so the CLI and the server call **one** finalize — the drift that caused this bug cannot recur. Repo *preparation* (name allocation) needs `internal/registry`, which imports `internal/publish`; to avoid a cycle it stays inline in `engineRunner`, mirroring the CLI's `newPublisher`. The CLI keeps its operator-facing extras (prior-reproduction warnings, the instrument report, the collaborator invite, stdout) around the shared call; behavior is unchanged.

**Resume needs no mount change.** A restart rejoins the live session by its run id (the reconciliation path). The repository already exists (`RepoName` is durable), the agent is already committing to the same mount, so `Resume` → `Publish` finalizes with no re-creation and no re-mount.

**Repository visibility stays private on creation.** A reproduction is never born public; the owner's signed public flip (006f) — already wired — is the only way it goes public, and now there is finally a repository for it to flip.

## Acceptance criteria

1. A web-submitted audit reaches `done` with `job.RepoName` set to `open-hai/<slug>-<suffix>`, and the repository exists on the forge with the agent's commit history.
2. The repository carries `replication.json` (the system-authored manifest: verdict, method digest, redistribution-clear) and the committed transcript (005), so the session's Verdict, Artifacts, and Transcript tabs populate through the existing read surface.
3. Resolve records the paper identity into the repo; Publish reads it back and does **not** re-resolve over the paper source (no arXiv rate-limit dependency at publish time).
4. A restart mid-run rejoins the live session and finalizes against the already-created repository — no duplicate repo, no lost `repo_name`.
5. The CLI (`audit --publish`, `finish`) produces byte-identical published output through the shared `publish.FinalizeLive`; its existing tests pass unchanged.
6. `FinalizeLive` is covered against a fake `LiveForge`: a clean tree writes a manifest marked redistribution-clear and commits the transcript; a tree with a third-party path writes the manifest **not** clear (barring the public flip) and still commits.
7. The server runner path is covered against fakes (no network): Resolve creates-and-names the repo and sets `RepoName`; Execute passes a mount built from it; Publish finalizes through the shared helper.

## Out of scope

- **The two-token mount split** (a repo-scoped GitHub App token in the sandbox). The egress-held credential makes it unnecessary for secrecy now; it stays 006f's hardening for untrusted submissions.
- **A per-project repo opt-out at run time.** Every server audit creates a repository. (The public-flip `flip_repo` opt-out — keep the repo private while the project goes public in-app — is a separate, existing concern.)
- **The instrument report** in the shared helper. It stays CLI-only (a report, not a gate); the server omits it.
- Any change to the read surface (006f/006h) — this spec only makes the repositories those handlers read finally exist for server sessions.

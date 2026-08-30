---
title: "The server harvests what the session produced"
status: built
---

# 044b — The server harvests what the session produced

**Status:** built.

**Scope:** the server `Retain` hook commits the session's output files into the mounted repository, through `VerifyTree`. One new function in `internal/publish`, one hook change in `cmd/server/runner.go`.

**Depends on:** [044](044-a-run-that-produced-nothing.md), [006i](006i-server-side-mount-and-publish.md), [009](009-secret-containment-when-the-agent-commits-live.md).

## Problem

There are two ways out of a managed session: the mounted repository, and the output directory the runtime uploads as session files. The CLI reads both — `collectManagedArtifacts` in `internal/audit/session.go` fetches every artifact through `EvidenceReader.Fetch` and writes it to the destination directory. The server reads one:

```go
Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
        …
        _, err = publish.FinalizeLive(ctx, e.gh, owner, name, id, e.method.Digest(), grade, providerEvents(evidence.Events), time.Now)
        return err
},
```

`evidence.Artifacts` is bound and never read. In the incident the agent had copied `README.md`, `REPRODUCIBILITY.md`, `verdict.json`, `instrument.json` and `src/` into the output directory; every one of them reached the session's file scope; and the server dropped all of them on the floor a few seconds before releasing the sandbox that held the only other copy.

## What changes

The `Retain` hook harvests before it finalizes:

```
evidence.Artifacts ──▶ Fetch each ──▶ VerifyTree(paths)
                                          │
                          flagged ────────┴──────── clean
                             │                        │
                        left out,               CommitFiles
                        named in the report     "chore: reproduction outputs"
                                                        │
                                                 FinalizeLive(…)
```

`publish.CommitOutputs(ctx, forge, owner, name, files)` is the new seam: it takes the fetched bytes, runs their paths through `publish.VerifyTree`, commits the clean ones in one commit, and reports what it committed and what it refused. It is idempotent through `CommitFiles` — a re-finalize of a repository that already holds them adds no commit.

**Order matters and is deliberate.** Harvest first, finalize second. `FinalizeLive` reads `verdict.json` from the committed tree, so a recovered `verdict.json` has to be in the tree before the manifest is written, or the manifest records a findings problem the repository does not have. 044d's empty-repository check reads the same tree and depends on the same ordering.

**A path the agent pushed wins.** `CommitFiles` overwrites, so an output-directory copy would replace a pushed file of the same name. The harvest therefore skips any path the committed tree already holds: what the agent pushed is its considered work, and what it copied out is a snapshot that may predate its last revision. In the incident the tree held nothing, so everything is recovered; in a healthy run the harvest is a no-op.

**The boundary holds.** 006f made the mount the redistribution boundary — only what lands in it is published. Files arriving from the output directory have never passed that boundary, so they pass `VerifyTree` on the way in, exactly as the committed tree does on the way out. A flagged path is refused and named; it is never committed and then reported.

## Acceptance

- `publish.CommitOutputs` commits clean files in one commit, refuses flagged ones, skips paths already in the tree, and is a no-op for an empty set.
- The server's `Retain` hook harvests through it before calling `FinalizeLive`, and a fetch failure for one artifact does not abandon the rest.
- A harvest that recovers `verdict.json` produces a manifest with `findings.present = true` and no `UNVERIFIED.md` note.
- A harvested `paper.pdf` is refused, named, and absent from the tree.
- An e2e test over the fake forge and a fake runtime reproduces the incident — an agent that pushed nothing and copied everything out — and ends with the reproduction in the repository.

## Not in scope

The CLI path, which already harvests to a local directory. Compiled artifacts, which `collectManagedArtifacts` skips and this does too.

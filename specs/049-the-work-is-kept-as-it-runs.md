---
title: "The work is kept as it runs"
status: built
---

# 049 — The work is kept as it runs

**Status:** built for the command line: the two defects under Problem are fixed, the controller takes snapshots, and a command-line run keeps each one in its output directory and, when it publishes a hosted session, in its repository. The server's snapshots and the Latere platform's saving during a turn are designed and not built.

**Scope:** while a session runs, the files it has produced so far leave the runtime every few minutes, so a session that fails, times out or is cancelled late still leaves its work behind. Where the work goes: the output directory of a command-line run, and the reproduction's repository when the run publishes. Which runtimes can hand their files over mid-run, and what each does until it can.

**Depends on:** [041](041-the-runtime-is-not-the-model-vendor.md), [041b](041b-one-managed-session-lifecycle.md), [041g](041g-latere-runs-a-session.md), [044](044-a-run-that-produced-nothing.md), [045g](045g-the-host-pushes.md), [009](009-secret-containment-when-the-agent-commits-live.md).

## Problem

A session's work leaves the runtime once, at the end, in the controller's Retain step. Everything before that point lives only inside the runtime. On 2026-10-16 an audit on the Latere runtime ran for an hour, hit the command line's time limit, and kept nothing: no artifact, no transcript, no repository commit. A run that dies at minute 59 loses 59 minutes of work, and the person learns that only at the end.

Two defects made that run's loss total, and both are fixed:

- **A cancelled Latere session was reported ended at the cancel mark.** The platform saves a turn's files as the turn ends, and an interrupted turn ends some seconds after the interrupt. The adapter reported the session ended before that, so the controller read the session's folder before the save and found nothing of the turn. A cancelled session is now reported ended once its interrupted turn has stopped, or after a bounded wait.
- **Handing over the evidence shared the cleanup budget.** Cancelling, waiting for the session to stop, reading its transcript, fetching every file and pushing them all ran on one thirty-second budget. A run with a working directory of any size could not hand it over in that time. The handover now has its own budget, ten minutes by default, on a context detached from the caller's, so a run that ends at its deadline or is cancelled still hands over what it produced.

Those fixes keep the work of a run that ends. They do nothing for a run that is killed outright, a machine that is lost, or a person watching a long run who wants to see what it has done so far.

## Design

### A runtime says whether it can hand over files mid-run

`Capabilities.LiveArtifacts` reports that `ListArtifacts` and `FetchArtifact` may be called while the session runs and return what the session has saved so far. A runtime that does not declare it refuses them before the session ends, as the contract already asks.

| Runtime | Declares | What a mid-run listing holds |
|---|---|---|
| `local` | yes | the session's working directory on the operator's machine, as it is |
| `latere` | yes | what the platform has saved of the working directory, which advances at the end of each turn; once the platform saves during a turn (below) it advances during one too |
| `memory` | when configured | its configured files |
| `anthropic-managed` | no | with a mounted repository the agent pushes as it works, so the work already leaves as it is made; without one, nothing until the end |

### The controller takes a snapshot every few minutes

`Hooks.Snapshot` receives the same evidence view as `Retain` (the observation, the transcript so far, the artifact list, and a fetch), every `WaitPolicy.SnapshotEvery` (five minutes by default) while the session runs, and only when the runtime declares `LiveArtifacts`. A snapshot runs between polls, on a bounded context of its own. A failed snapshot is reported and skipped: it never fails the run and never releases the session. `Retain` stays the one handover that gates release, and it runs after the last snapshot, so the final state always wins.

### The command line keeps each snapshot

- **Output directory.** Each snapshot writes the session's files into the run's output directory, as the final handover does, and the transcript so far into `<out>/<paper>-transcript.jsonl`, a sibling of the directory so it is never mistaken for one of the session's files. The final handover writes both again. A run that fails, times out or is cancelled leaves its last snapshot, and the final handover then writes over it with whatever the session kept until the end. A file the session deleted after a snapshot stays in the directory; the directory holds the latest of each file, not a mirror.
- **Repository, for a hosted session that publishes without a mounted repository** (the `latere` runtime today). The first snapshot creates one local repository for the run. Each snapshot writes the session's files into it, commits only when something changed, checks the committed tree with the same gate the final push uses, and pushes. The final handover commits on top of the same history and pushes as before, so the repository shows the work arriving as the session makes it, and a run that dies keeps everything up to the last snapshot.

A snapshot that the gate refuses is not pushed, and the run carries on; the final push reports the refusal as it does today. A mounted repository needs none of this, since the agent pushes into it as it works, and a `local` session's workspace stays on the operator's machine whatever happens to the run, so it is pushed once, at the end, as before ([045g](045g-the-host-pushes.md)).

### The server

The app's job runner keeps its sessions the same way: a snapshot commits the session's output directory to the reproduction's repository on the same interval, through the same gate. Not built yet.

### The Latere platform saves during a turn

The platform writes a session's working directory to its folder at the end of each turn. A Latere session that works through one long turn therefore has nothing saved until that turn ends, and a mid-run listing shows the previous turn's files. Topos spec 076, in the public Topos repository, has the runner save the working directory during a turn as well. Until it ships, Latere snapshots advance at turn ends.

## Acceptance criteria

1. A cancelled Latere session is reported ended only once its interrupted turn has stopped, or after a bounded wait, and its files are then listable. (Built.)
2. The evidence handover has its own budget, detached from the caller's, and completes after a cancel even when it takes longer than the cleanup budget. (Built.)
3. `Capabilities.LiveArtifacts` is declared by `local` and `latere`, not by `anthropic-managed`; the contract suite lists and fetches a declaring runtime's files mid-run. (Built.)
4. The controller calls `Hooks.Snapshot` at the configured interval while a session runs, only for a runtime that declares live artifacts; a failing snapshot neither fails the run nor prevents `Retain`. (Built.)
5. A command-line run's output directory holds the session's files, and its transcript file sits beside it, after each snapshot and after a run that fails or times out. (Built.)
6. `replichai audit` publishing a hosted session pushes a commit for each snapshot that changed something, through the publication gate, and the final push lands on top of that history. (Built.)
7. The server keeps snapshots in the reproduction's repository. (Not built.)
8. The Latere platform saves the working directory during a turn (Topos 076). (Not built.)

## Outcome

Built on 2026-10-16 for the command line, with the two defects fixed first. Verified against the memory runtime, the Latere adapter's stand-in platform (a cancelled turn now saves before it is read), the local runtime's contract suite, and a bare repository as the push target. Not yet verified on a live Latere session: the owner holds further live runs for now.

## Not in this spec

Showing snapshots in the app's session view; resuming a session from a snapshot; keeping more than the latest state in the output directory.

---
title: "Latere runs a session"
status: designed
---

# 041g — Latere runs a session

> **Rewritten 2026-10-15** against the platform as it runs today (Topos v0.41.0 behind `api.latere.ai/v1/agents`). The first design, of 2026-09, embedded Topos's agent loop in ReplicHAI's own process and composed Lux and Cella itself, and was blocked until that embedded loop could survive a crash. The platform now runs the loop as a hosted service, so the adapter is a client of it, the blocking question is answered by the platform, and most of the first design's machinery is gone. Where this text and the first design disagree, this text holds.

**Status:** designed. The command-line half can be built and run now. The app's half waits on Auth (048, Price and charge). A study's runs, which are handed read-only inputs (046), wait on Topos delivering a read-only input (the public Topos repository's spec 075); until it is built the runtime refuses them, and a study that fixes its runs' network waits on the platform letting a session's agent narrow it, a change written up on the platform's side.

**Scope:** `latere`, a managed runtime that runs one ReplicHAI session as a hosted Topos session on the Latere platform, on any model the platform serves, charged to the person who starts it. It satisfies `managed.Runtime` and passes the shared conformance suite. Choosing it is [048](048-the-runtime-is-a-choice.md).

**Depends on:** [041e](041e-anthropic-conforms-to-the-session-contract.md), [048](048-the-runtime-is-a-choice.md), the platform's Agents API ([platform.latere.ai/docs/agents](https://platform.latere.ai/docs/agents)), and Latere Auth.

## Why it is a client of the platform

The Latere platform is a composition: Topos runs the agent and keeps the session, Cella runs the sandbox, Lux routes the model and meters it, Auth says who is acting, and platformd decides what they may do and charges their wallet. The family's rule for products is that an application calls a job, and only a job composes resources. Topos is the job. So the adapter talks to Topos's API, reads the session's own folder in Storage, and reaches nothing else: it never creates a Cella sandbox, never calls a Lux door, and holds no model provider key.

That one choice settles what the first design could not:

- **Durability is the platform's.** A hosted session's loop runs on the platform's runners, which hold it by a lease and fence their writes; a runner that dies hands the session to another within a minute, and the session resumes from its log. ReplicHAI keeps only the session's id, and every later call (status, events, interrupt, end) is keyed by it, so a ReplicHAI process can restart at any point and pick the session up. The first design's gate (a crash in the middle of a model call or a command must not repeat it) has moved to the platform rather than closed: Topos recovers from a lost runner and closes an interrupted tool call as of unknown effect, and its own test of a kill at every commit point is not built yet.
- **Inference and metering are the platform's.** Every model call is recorded on the session with the model that served it, its token counts and its cost.
- **The sandbox and its network are the platform's.** Topos opens a machine at the agent's first tool call and closes it when idle.

## The shape of a session

```
SessionSpec ─▶ latere adapter ─▶ api.latere.ai/v1/agents
                  │                 PUT  /agents/{name}            the worker, once per distinct manifest
                  │                 POST /sessions                 one Topos session per ReplicHAI session
                  │                 PATCH /sessions/{id}           the chosen model
                  │                 POST /sessions/{id}/events     the task; later, each grading and revision round
                  │                 GET  /sessions/{id}            state and spend
                  │                 GET  /sessions/{id}/events     the transcript and per-call usage
                  │                 GET  /sessions/{id}/files      a findings file, while the machine runs
                  │               api.latere.ai/v1/storage
                  │                 GET  /files/{owner}/{folder}/work/...   the outputs, after each turn
                  │
                  └─ SessionRef { runtime: latere, id: <topos session id>, locator: { stage, round, seq } }
```

`SessionRef.ID` is the Topos session id. The locator records the grading stage and round and the event sequence each stage began at, and nothing else: no token, no task text. Every advance of the stage is a new locator revision, persisted by the controller's checkpoint before the adapter's next call (041b).

**The worker.** The adapter applies one agent in the person's own context, named from a digest of its manifest (`replichai-<digest>`), so the same method and runtime configuration reuse one agent and a changed one gets its own. Its manifest carries:

- the session's instructions;
- the built-in tools a reproduction needs (read, write, edit, bash, grep, glob, web_fetch, todo), and the grader below as its one subagent;
- the Cella permissions a machine needs (`cella:sandbox.*`, `cella:secret.*`, `cella:environment.use`), without which the first tool call is refused;
- approvals in `progressive` mode with every built-in tool always allowed, because a session nobody attends would otherwise stop on a confirmation forever;
- the platform's base image, and a turn timeout long enough for a reproduction, since the default two hours ends a long turn.

The session is created with `attended: false`, so a question the agent asks is answered at once and never stops the run. Each of these is checked against the live platform before the adapter relies on it.

**The model.** A session is created without a message, its model is set with one `PATCH` to the chosen Effort level (`tier/quick`, `tier/thorough`, `tier/sustained`) or to the specific model the person picked, and only then is the task sent. The platform refuses a model the person may not use, with its own reason, before any model call.

**The task.** One `user.message` carries the session's task and its attachments. Topos writes attachments into the working directory when the machine opens.

## Grading

Topos has no grader of its own. It has the piece one is built from: a **subagent**, declared in the worker's manifest, which runs as a thread of the session in a context of its own, with only its own instructions and tools, on the same machine and therefore reading the same files. The adapter composes the grading loop from it, as the local runtime composes its judge stage from a second run of the harness.

The grader subagent holds the judge's instructions and read-only tools (read, grep, glob), so it can read the work and cannot change it. A round is:

1. The worker's turn ends.
2. The adapter sends the worker a short message asking it to spawn the grader with the round's task, which carries the rubric and the findings check.
3. The worker calls `spawn`. The adapter reads the thread's start from the events and checks that it is the declared grader, with exactly the round's task and only the read-only tools; anything else fails the attempt.
4. The grader reads the work and ends its turn with the verdict as a JSON object in its final message, in the shape the local runtime's judge writes (grade, explanation, one row per criterion). The adapter reads that message from the grader's own thread, never from the worker's relay of it, and validates it as the local runtime validates its judge's file. A grader that answers anything else has failed the attempt, and the adapter asks for another spawn, up to the same attempt limit the local runtime uses.
5. Passed ends the session done. Needs-revision with revisions left sends the worker the verdict's failed criteria as its next task; otherwise the session ends failed with `evaluation_failed`.

The worker never writes the verdict: the adapter reads it only from the grader's thread. The grader is a separate context that reads the work and the rubric, not the worker's account of it, which is what 041a and the managed-agent port require of a runtime that declares `Evaluation`.

Two facts of the platform, both found by a probe on 2026-10-16, shape this. A subagent's model must be a model, not an Effort level, since the platform resolves a level only for the session's own model, and a free model cannot be named at all; so the grader runs on the first paid model of the session's level, or on the specific model the person chose. And a client tool, which would have made the verdict a schema-checked call rather than a message, is not offered to a subagent's thread; if Topos offers it later, the verdict moves to it without changing what a round is.

When `EvaluationSpec.Findings` names a findings file, the adapter reads it after each worker turn, checks it against its schema, and puts the problems in the grader's task and in the revision, as the local runtime does (`FindingsRounds`).

## What the platform does not give, and what follows

Each is either refused before anything remote is created, as 041a requires, or is a platform change written up in that platform repository. None is worked around inside ReplicHAI.

| The specification asks for | What the platform does today | The adapter |
|---|---|---|
| A network policy (`none`, an allowlist, `open`) per session | The network is the person's own account preference (listed hosts, package managers, ask, or open), for every session they start; a session cannot choose another, and under an allowlist the platform joins its own hosts to any the agent names | Declares `open` when the person's preference is open, read before any remote write, and nothing otherwise, so an audit is refused with a message naming the preference. An allowlist is not offered: the platform's joined list is wider than the one asked for, and a study must record the network its runs actually had. A session whose agent narrows the preference is a change on the platform's side, written up there |
| Packages | No package or bootstrap field; the base image carries Python with the document and data libraries | Accepts `pip` packages the configured image already provides and refuses any other |
| Bootstrap commands | Not built (manifest hooks are validated and never run) | Refuses |
| A read-only input outside the workspace (`InputResource`) | Attachments are written into the working directory, where the agent can change them | Declares no `InputDir`, so a study's fixed inputs are refused. Read-only inputs are Topos's spec 075 |
| A repository the agent pushes to with a ReplicHAI credential | No client-supplied secret (named secrets are not built); GitHub only through the person's own GitHub connection, on `agents/*` branches | Declares no repository modes. The host publishes the outputs to GitHub afterwards, as it does for the local runtime ([045g](045g-the-host-pushes.md)) |

## Outputs and the host's publish

A Topos session's machine is stopped five minutes after its last tool call, but its files outlive the machine. At the end of every turn that changed files, Topos writes the working directory's latest state as plain files under `work/` in the session's own folder in Storage, in the person's own space, and the session names that folder. The adapter lists and reads the outputs there (`GET /v1/storage/files/{owner}/{path}`, with `?list=1` for the listing), so they are complete, independent of what the agent remembered to mention, and readable long after the machine is gone. The same token that reaches Topos reaches Storage.

No forge credential ever reaches the platform. The host (the command line, or the server's worker) takes the files the adapter fetched, builds the reproduction repository locally, verifies the tree, and pushes it with its own GitHub credential, which is the path the local runtime already takes.

## Usage and cost

Each `model.request` event carries the model that served the call, its token counts and its cost in micro-USD. The adapter normalizes each into an event usage delta: input excludes cached input on this wire, so fresh input is `input_tokens`, cached input `cache_read_input_tokens`, cache write `cache_write_input_tokens`, output `output_tokens`, and reasoning tokens a detail of output. Cost is carried as the extra meter `charged.cost_usd_micro`, which no rate card prices: a Latere job is paid by the person (048), its settlement never calls the card, and the job records the meter as the session's reported cost. Status usage is the cumulative sum, which is monotonic by construction. `Status.Model` is the model the session started on, as the platform resolved it when the session was created; a turn the platform moved to another model of the level names that model on its own calls.

The platform charges the person's wallet for every call (048). ReplicHAI does not price the session; it records what the platform reported.

## Identity

Every call to the platform carries a token for `api.latere.ai` that acts as the person who started the session, minted fresh for each call from their own sign-in:

- **On the command line**, from the person's personal access token (`LATERE_API_KEY`), exchanged at Auth for a fifteen-minute token; later from `replichai login` once the CLI's client may mint for `api.latere.ai`.
- **On the server**, from the person's stored refresh token, as 048 describes, once Auth allows it.

A token never enters a locator, an event, a log line or a job row.

## Cancellation and release

`Cancel` sends `user.interrupt`; the session goes idle at the next step boundary and the adapter reports it failed with `cancelled`. `Release` ends the session (`POST /sessions/{id}/end`) and then deletes it, which removes its machine; the transcript ReplicHAI kept in `Retain` is the record. Both are idempotent, and a session already ended or already gone is success.

## Configuration

| Variable | Meaning |
|---|---|
| `LATERE_API_URL` | the platform's API root, `https://api.latere.ai` by default |
| `LATERE_AUTH_URL` | Latere Auth, `https://auth.latere.ai` by default |
| `LATERE_API_KEY` | command line only: the person's personal access token |
| `LATERE_IMAGE_PACKAGES` | optional: the `pip` packages the machine image provides, when it is not the base image |

## Acceptance criteria

1. The adapter passes the shared conformance suite against a stub of the platform's API, including restart, graded and ungraded sessions, cancellation keeping evidence, and release.
2. A specification asking for a network mode the person's preference does not give, a package the image lacks, a bootstrap command, an input or a repository is refused before the first request that changes anything on the platform.
3. A round's verdict is accepted only from the declared grader's thread, started with exactly the round's task and read-only tools; a verdict in the worker's own messages, or a grader started with another task or other tools, fails the attempt.
4. Usage tests prove the normalization from a recorded Topos transcript: fresh, cached, written and output tokens, reasoning as a detail, cost as a meter, cumulative and monotonic.
5. No token appears in a locator, an event, an error or a log line.
6. `replichai audit <paper> --runtime latere` runs a real paper on the live platform, graded, and publishes its reproduction.

## Out of scope

- Calling Lux, Cella or Origo directly.
- Writing to Storage, or keeping anything there beyond what Topos keeps for every session. The reproduction repository on GitHub is the durable record.
- Multi-agent work beyond the one grader subagent.

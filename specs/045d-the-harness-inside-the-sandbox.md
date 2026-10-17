---
title: "The harness inside the sandbox"
status: built
---

# 045d — The harness inside the sandbox

**Status:** built.

**Scope:** the `Harness` seam inside `local` (how one agentic coding CLI becomes argv, environment, configuration files, and a decoder from its stream to `managed.Event`) and two adapters, Claude Code and Codex. Model endpoints and credentials are [045e](045e-the-model-is-an-endpoint.md); the judge stage is [045f](045f-the-grader-cannot-edit-what-it-grades.md).

**Depends on:** [045a](045a-the-sandbox-is-a-seam.md), [045b](045b-the-host-sandbox.md), [005](005-transcript.md) (what a transcript owes a reader).

## Problem

A sandbox is not an agent. Something inside it has to read the task, use tools, edit files, and report what it did, and there are several such programs, each with its own flags, its own streaming format, its own idea of a system prompt, and its own usage accounting. The runtime needs exactly one of them per session and must not care which.

The wrong answer is a `managed.Runtime` per harness. Runtime names are persisted in `SessionRef` and route every later poll; `local-claude-code` and `local-codex` would double again with the second sandbox driver, and each name would carry a capability declaration for capabilities that belong to the driver, not to the harness.

## The seam

One interface, inside `internal/runtimes/local`, not exported to core code:

```go
// Harness is one agentic CLI the runtime can drive inside a sandbox. It
// contributes what is specific to that program: how it is invoked, what
// environment and configuration it needs, and how to read what it says. It
// owns no lifecycle — the sandbox driver starts and stops stages.
type Harness interface {
    // Name is the canonical harness name, as an operator spells it.
    Name() HarnessName

    // Detect reports the installed version, or a NotReadyError naming how to
    // install it. Under the container driver it reads the image label;
    // under the host driver it runs the binary.
    Detect(context.Context, Sandbox) (Version, error)

    // Argv is what one stage runs.
    Argv(stage Stage, spec managed.SessionSpec, model managed.ModelRef, paths Paths) ([]string, error)

    // Env is the environment the stage needs. Values come from runtime
    // configuration and never from the specification, because a spec that
    // sets NetworkOpen may not carry secrets (managed/validate.go).
    Env(stage Stage, model managed.ModelRef, gateway Gateway) (map[string]string, error)

    // Files are configuration files written before the stage starts, always
    // outside the workspace.
    Files(stage Stage, model managed.ModelRef, gateway Gateway) ([]File, error)

    // Decode turns one line of the harness's stream into zero or more
    // normalized events. An unrecognised line becomes one event of kind
    // EventRaw carrying the line, never nothing: a transcript that silently
    // drops what it does not understand is not evidence.
    Decode(stage Stage, index int, line []byte) ([]managed.Event, error)
}
```

`Stage` is `worker`, `revision`, or `judge`. `Paths` carries the workspace and, for the judge, the read-only evidence path and the grade path.

Selection is `--harness` on the CLI, `REPLICHAI_HARNESS` in the environment, or a field in the batch file, defaulting to `claude-code`. An unknown name lists the installed ones, as `runtimes.UnavailableError` does.

`Detect` is what keeps the host driver's declaration accurate: under it the harness is whatever the operator installed, so its version is discovered, recorded in `environment.json`, and a missing binary is a preflight failure with install instructions rather than an exec error forty seconds into a run.

## Instructions and task

`SessionSpec` separates `Instructions` (the worker's durable role) from `Task` (this session's request), and the contract permits an adapter to combine them on the wire but not to reorder them or change what they mean.

Every harness gets the instructions through its own system-prompt mechanism, never through a file inside the workspace. A `CLAUDE.md` or `AGENTS.md` written there would be committed by the agent and published as part of the reproduction, which makes the method's own text an artifact of the paper's reproduction. Where a harness has no system-prompt flag, the adapter prepends the instructions to the task under a fixed header and records that it did so in the transcript's first event, so a reader can see which form was used.

Harness-level configuration (a model provider block, a permission mode) goes to the harness's own config directory, which under the host driver is a session-scoped copy on `HOME`-adjacent config paths the sandbox allows, and under the container driver is the image's home. Neither is the workspace.

## Claude Code

```
claude --print
       --output-format stream-json --verbose
       --model <resolved model id>
       --append-system-prompt <instructions>
       --permission-mode bypassPermissions
       --add-dir <workspace>
       -- <task>
```

`bypassPermissions` reflects where the boundary is: the sandbox is the boundary, and a permission prompt inside a non-interactive run only stalls it. That is why [045b](045b-the-host-sandbox.md)'s path and network rules are the ones that matter, and why the harness's own permission model is not asked to be a second, weaker copy of them.

The stream is one JSON object per line:

| Stream line | `Kind` | `Role` | Content |
|---|---|---|---|
| `system` / `init` | `EventSystem` | `RoleSystem` | model, tools, cwd |
| `assistant` with text | `EventMessage` | `RoleAssistant` | the text |
| `assistant` with `tool_use` | `EventToolCall` | `RoleAssistant` | `Tool` set, input serialized |
| `user` with `tool_result` | `EventToolResult` | `RoleTool` | the result, truncated per below |
| `result` | `EventResult` | `RoleSystem` | stop reason, totals |

Usage rides on the `assistant` and `result` lines: `input_tokens`, `cache_creation_input_tokens` (with its `ephemeral_5m`/`ephemeral_1h` split where present), `cache_read_input_tokens`, `output_tokens`, mapping onto `Usage.InputFresh`, `CacheWrite5m`/`CacheWrite1h`, `InputCached`, `Output`. The `result` line's cumulative totals are authoritative; per-message numbers accumulate only until a `result` supersedes them, so the monotonicity the contract requires holds without arithmetic that could double-count.

Credential: the harness's own login where one exists, else `ANTHROPIC_API_KEY`, else `ANTHROPIC_AUTH_TOKEN` with `ANTHROPIC_BASE_URL` for a gateway ([045e](045e-the-model-is-an-endpoint.md)).

## Codex

```
codex exec --json
     --model <resolved model id>
     --sandbox danger-full-access
     --cd <workspace>
     -- <task>
```

`danger-full-access` says the same thing `bypassPermissions` does: the harness's own sandbox is switched off because it is nested inside a real one, and a second boundary that the model can be talked past adds nothing. The runtime refuses to run a harness in this mode outside a sandbox at all: there is no code path in which the flag is set and no driver is confining the process.

Instructions and the provider block go to Codex's configuration file, outside the workspace:

```toml
model_provider = "replichai"
[model_providers.replichai]
name = "replichai gateway"
base_url = "https://<gateway>/v1"
env_key = "REPLICHAI_MODEL_KEY"
wire_api = "responses"     # or "chat", per 045e
```

The `--json` stream decodes onto the same event kinds. Where a reported field has no equivalent bucket it goes to `Usage.Details`, which is explicitly non-billable decomposition, rather than into a bucket it does not belong in.

## Event identity, and what a re-read must not change

`managed.Event.ID` must be stable: re-reading the transcript neither duplicates entries nor changes what one means. Harness streams do not reliably carry ids, so the adapter derives one:

```
ID = "<stage>-<sequence>"          sequence = the line's ordinal within that stage's stream
```

Position, not content: a hash of the line would give two identical tool results the same id, and the transcript is a sequence. `session/events.jsonl` is append-only and the pump records how far it has read, so a `Poll` after a restart resumes from the last id rather than re-decoding from the start.

`Raw` keeps the harness's own line verbatim, for the reason the contract gives for that field: the transcript is the evidence for a run and must not be reduced to this package's vocabulary. A tool result larger than 256 KiB is truncated in `Content` with an explicit marker and kept whole in the raw stage log under `session/raw/`, so the normalized transcript stays readable and nothing is destroyed.

## Where a harness must not reach

The worker stage has network access and a shell, which is what the artifact hunt needs. Three things are kept out of it rather than protected inside it:

- **No forge credential.** There is no remote and no token ([045g](045g-the-host-pushes.md)).
- **No writable `session/`.** The transcript is written by the host, from a stream the stage cannot reach ([045a](045a-the-sandbox-is-a-seam.md)).
- **No cloud credential.** `~/.config/replichai` is on the deny list ([045b](045b-the-host-sandbox.md)) and is not mounted under the container driver.

The model credential is the one that has to be inside, because without it the harness does nothing.

## Acceptance criteria

1. `Harness` has exactly one implementation per supported CLI and no method that launches, observes or stops a stage.
2. Fed a recorded Claude Code stream-json fixture, the decoder produces events whose ids are stable across two full decodes and whose accumulated usage equals the fixture's own `result` totals, bucket by bucket. The same test exists for Codex.
3. An unrecognised line becomes exactly one `EventRaw`, and a test asserts no input line is dropped: decoded events carrying a non-empty `Raw` equal the input line count.
4. No adapter writes any file inside the workspace. A test inspects `Files()` for every stage and every harness and asserts every destination is outside it.
5. Instructions reach the harness through a system-prompt mechanism or a recorded fixed-header prepend, and a test asserts the instructions text precedes the task text in whichever form was used.
6. A tool result over the truncation limit appears truncated in `Content`, whole in the raw stage log, and marked in both.
7. `Detect` reports the installed version into `environment.json`, and a missing harness produces a `NotReadyError` with install instructions, not an exec failure.
8. No code path sets `bypassPermissions` or `danger-full-access` without a sandbox driver confining the stage; a test asserts it against the constructed `StageSpec`.
9. `--harness` with an unknown name lists the installed harnesses.

## Not in scope

- A third harness. The seam is the deliverable; adding one is a table entry afterwards.
- Interactive attach to a running harness. Sessions are non-interactive.
- Teaching a harness the method. `method/` is data and reaches the harness as instructions, unchanged.
- MCP servers inside the sandbox.

## Outcome

Built as `internal/runtimes/local/harness`, and then corrected against both real binaries. Event ids come from the stage's unique name rather than its role, because a session that grades twice would otherwise issue one id twice, and every line of a recorded stream produces at least one event carrying its raw payload, asserted by counting.

**`Detect` became `VersionArgv`, and then `Needs`.** This spec put version detection on the harness; it belongs to the driver, because where the harness lives differs by driver. What the harness does own turned out to be larger and was not in this spec at all: a harness is a real installation with real requirements of the machine, and three of them are not guessable from outside. Where the operator's existing login lives: a file for Codex, the macOS Keychain for Claude Code. Where its own scratch area is, without which Claude Code's shell tool fails before running anything. And whether it will trust a certificate authority injected by the sandbox: Node does, the Rust binary does not. `harness.Needs` is where a harness declares these, and the runtime grants exactly that.

**Both adapters had the same bug.** Each redirected the harness's own configuration directory to a session-scoped one, which is how a session gets an isolated configuration and also how it loses the login: an empty directory holds no credential, and the harness answers "not logged in". The redirect happens only where a credential is supplied instead.

**The Codex decoder was written against a guessed format.** Its real stream is a flat envelope (`thread.started`, `turn.started`, `item.completed` carrying an item, `turn.completed` carrying the usage) and the guessed one read none of it, so every Codex session reported zero tokens. The fixture is a recorded run now, and the older envelope is still accepted.

**Codex also refuses a directory it has not been told to trust.** A session's workspace is a repository it has never seen, so the trust check is skipped: the sandbox is the boundary, and a second one the model can be talked past adds nothing.

**A harness's standing instructions are withheld.** `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md` are denied to every run. This spec was careful that the method never reach the workspace as a file; it did not consider that the operator's own instructions would reach the agent through its configuration directory. A reproduction that silently inherits the habits of whoever ran it is not reproducing the paper.

**A run reads the login, not the history (2026-10-17).** A session on the operator's own login used to be granted the harness's whole configuration directory: `~/.codex` read and write, or `~/.claude` read-only. Those directories also hold the operator's own conversation history (codex's `sessions/`, `archived_sessions/`, `history.jsonl`, memories and logs; Claude Code's `projects/` and `history.jsonl`), so an agent steered by a paper's content could read it. Now Codex runs with `CODEX_HOME` in the session and the operator's `auth.json` linked in, and that file is the whole login grant, writable so a refreshed token is saved where the operator's own Codex reads it. Codex's standalone installation under `~/.codex/packages` is granted read-only to every stage, because the program and the links to it live there. Claude Code keeps its default configuration directory, because its Keychain entry is named after it, and is granted only `~/.claude.json` and the Keychain on macOS, or `~/.claude/.credentials.json` elsewhere. `TestALoginStageUnderSrtReadsTheLoginAndNoHistory` runs both harnesses' stand-ins under the real srt against a fake home with planted history, and a live session per harness passed on the operator's login.

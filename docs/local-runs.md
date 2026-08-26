# Running audits on your own machine

`replichai audit` normally sends the work to a hosted agent backend. With
`--runtime local` it runs here instead: the coding agent you already have
installed, confined to one folder, on whatever model you point it at.

Everything else is the same. Same method, same rubric, same seven output files,
same verdict. What changes is who owns the machine, whose model credential is
used, and who gets billed — which is nobody, because you bought the compute
from the vendor yourself.

```bash
replichai audit https://arxiv.org/abs/2603.01795 --runtime local
```

## What you need

One coding agent and one sandbox. On a machine where `claude login` has already
happened you need no API key and no configuration file.

**macOS**

```bash
npm install -g @anthropic-ai/claude-code       # the agent
claude login
npm install -g @anthropic-ai/sandbox-runtime   # srt, the sandbox
brew install ripgrep
```

**Linux**

```bash
npm install -g @anthropic-ai/claude-code
claude login
npm install -g @anthropic-ai/sandbox-runtime
sudo apt-get install -y bubblewrap socat ripgrep    # or dnf, on Fedora and RHEL
```

**Windows** is not supported. Run replichai inside WSL2, where both work.

If any of it is missing the run refuses before it fetches the paper, and prints
the commands that would fix it on your platform. Check what it found with:

```bash
srt "echo ok"
claude --version
```

## What a run does

Four steps, in order. Each is a separate process the runtime starts, watches
and can stop, so a failure says which one failed.

```
bootstrap   installs what the method declared — into the session's own
            environment, never the workspace
worker      the agent: reads the paper, hunts for artifacts, writes and runs
            the reproduction, commits as it goes
judge       the same agent again, in a fresh sandbox, with the workspace
            read-only and the rubric as its task
push        your machine, after the sandbox is gone: verify what was
            committed, create the repository, push it
```

The judge cannot edit what it grades — the workspace is mounted read-only, and
the only place it may write is a directory the worker never sees. That is a
filesystem permission, not an instruction the model is asked to respect.

## Where everything lands

Each session gets a directory under `~/.replichai/sessions/<id>/`:

| | |
|---|---|
| `workspace/` | your reproduction, and a git repository. This is what gets published |
| `tmp/` | downloads: the paper PDF, datasets, anything third-party |
| `venv/` | the language environment the bootstrap made |
| `grade/` | the judge's verdict, one file per grading round |
| `session/` | the transcript, the status, and what the run ran on. Nothing inside the sandbox can write here |

The split is the point. The agent works in `workspace/` and nothing else it
needs can end up in the repository; the transcript is written by replichai from
a stream the agent cannot reach, so the run cannot edit its own evidence.

Directories are kept after a run, because the workspace *is* the reproduction:

```bash
replichai sessions ls
replichai sessions prune --older-than 7d --dry-run
```

Set `REPLICHAI_SESSION_RETENTION=on-failure` or `discard` if you would rather
they cleaned themselves up.

## What the agent can and cannot reach

Inside the sandbox it can read and write its own session directory, read the
system toolchain, and reach the network. It cannot read your home directory
beyond one allowance: the agent's own configuration (`~/.claude`, `~/.codex`),
which is what makes your existing login work.

Explicitly denied, and tested: `~/.ssh`, `~/.aws`, `~/.config/gcloud`,
`~/.kube`, `~/.netrc`, `~/.gnupg`, `~/.docker/config.json`,
`~/.git-credentials`, `~/.config/replichai`, and any `.env` anywhere.

**No GitHub token ever enters the sandbox.** The agent commits into a folder
that outlives it, and your machine pushes that folder afterwards. That is why
`--publish` works locally without giving an agent write access to anything.

The one thing the read allowance costs: an agent that reads its own credential
file puts the contents in its transcript. Everything published is swept for
credential-shaped text first, and the transcript says so where something was
removed. It is a backstop, not a guarantee — read a transcript before you make
a repository public.

## Choosing a model

Write `~/.config/replichai/models.yaml`. With no file, the default is your
harness's own login on Anthropic's models, which is why a first run needs no
configuration.

```yaml
gateways:
  harness-login:                          # the login already on this machine
    dialect: anthropic
    auth: harness
  anthropic:
    base_url: https://api.anthropic.com
    dialect: anthropic
    key_env: ANTHROPIC_API_KEY
  lux:                                    # any OpenAI-compatible gateway
    base_url: https://lux.latere.ai/v1
    dialect: openai-responses
    key_env: LUX_API_KEY
  local:                                  # something you run yourself
    base_url: http://127.0.0.1:11434/v1
    dialect: openai-chat                  # no key_env: no credential is sent

models:
  - {provider: anthropic, id: claude-opus-5,    gateway: harness-login, harness: claude-code, preferences: [best]}
  - {provider: anthropic, id: claude-sonnet-5,  gateway: harness-login, harness: claude-code, preferences: [balanced]}
  - {provider: qwen,      id: qwen3-coder-480b, gateway: lux,           harness: codex,       preferences: [best]}
  - {provider: meta,      id: llama-4-scout,    gateway: local,         harness: codex,       preferences: [fast]}
```

Then pick one per run:

```bash
replichai audit <paper> --runtime local --model best
replichai audit <paper> --runtime local --model qwen/qwen3-coder-480b --harness codex
```

Rules the file is checked against when the run starts, so a mistake costs you a
message rather than forty seconds and a 400:

- **Dialect and harness must agree.** Claude Code speaks the Anthropic dialect;
  Codex speaks the OpenAI ones. An open-weights model behind an
  OpenAI-compatible gateway therefore pairs with `codex`.
- **One model per tier per harness.** Two `best` rows is an error, not a coin
  toss: what a session ran on has to be determinate.
- **A named credential must exist.** A gateway naming `LUX_API_KEY` with
  nothing in it fails now rather than mid-run.
- **An exact `--model provider/id` must be in the file.** A model you never
  declared has no endpoint to reach it through.

Your credentials are resolved once, passed to the agent out of view of the
process table, and removed from anything published.

## A pinned toolchain instead

`--sandbox container` runs the same session inside an OCI container — Podman
first, then Docker — for the one thing your own machine cannot give: the same
Python, the same packages, on every operator's machine. Use it for CI, or when
comparing your reproduction against somebody else's is the point.

```bash
replichai audit <paper> --runtime local --sandbox container
```

It costs an image pull, a virtual machine on macOS, and an API key instead of
your existing login. It also cannot filter the network by domain, which `srt`
can, so `--sandbox host` is the default.

Every run of either kind writes `environment.json` beside the transcript: the
operating system, the sandbox, the agent and its version, the model, and the
package versions actually resolved. Two reproductions that disagree can then be
told apart from two that merely ran on different interpreters.

## A whole project at once

```bash
replichai run papers.yaml --parallel 4 --publish
```

`papers.yaml` is one paper per line, or a list with per-paper overrides:

```yaml
project: chi-2026-audit
model: best
harness: claude-code
publish:
  owner: open-hai
papers:
  - https://arxiv.org/abs/2603.01795
  - doi: 10.1145/3654777.3676417
    model: fast
```

Each paper gets its own session, its own sandbox and its own repository. One
paper failing never stops the rest; the exit code is non-zero if any did, so CI
reads it and you read the report.

Two files are written beside the project file and kept current as it runs:
`batch.json`, the state, and `report.md`, the table — what passed, what it
cost, what did not happen. Interrupt it with Ctrl-C and nothing is lost:

```bash
replichai run papers.yaml --resume
```

Resume skips what finished and retries what failed, into the repository the
first attempt created.

## Putting the results in the app

Optional. A local run needs no account and no network beyond your model
endpoint. If you do want the sessions in `replichai.latere.ai`:

```bash
replichai login                              # device-code sign-in at auth.latere.ai
replichai project create "CHI 2026 audit"
replichai project ls
replichai push papers.yaml --project <id>
```

Nothing about an imported session is billed, and none of it is charged to a
project's credits: you paid the vendor. The record carries the paper, the
model, the usage, the verdict and the repository; the evidence stays in the
repository the run pushed, which is why you want `--publish` on a run you
intend to file.

Pushing twice is safe — a session updates its row rather than opening a second
— so a failed upload is retried by running the command again.

## When something goes wrong

**"the host sandbox is not ready"** — the message names what is missing and the
install command for your platform. It also offers `--sandbox container`, which
needs none of it.

**"no container engine is installed" / "podman machine is not running"** — on
macOS Podman needs a virtual machine: `podman machine init --cpus 4 --memory
8192 --disk-size 100 && podman machine start`.

**"harness ... is unavailable"** — the name is `claude-code` or `codex`, and
the message lists what is installed.

**The push failed** — the workspace is kept and the run prints a `finish`
command that pushes it. Nothing is lost: the work is on disk.

**The run committed nothing** — that is recorded rather than reported as
success. An empty repository is a real outcome, and the transcript in
`session/events.jsonl` says what the agent did instead.

## What is not there yet

- **Evidence is not uploaded to the app.** A session pushed without
  `--publish` arrives as a verdict with nothing behind it.
- **The container image is not published.** `--sandbox container` is built and
  needs an image that does not exist yet.
- **The app does not label an imported session** as locally executed, though it
  stores the fact.
- **`environment.json` does not record which domains a run reached.**

## Reference

| Flag | Default | What it chooses |
|---|---|---|
| `--runtime local` | `anthropic-managed` | run the session on this machine |
| `--sandbox` | `host` | `host` (srt) or `container` (podman, docker) |
| `--harness` | `claude-code` | `claude-code` or `codex` |
| `--model` | `best` | a tier (`best`, `balanced`, `fast`) or `provider/id` |
| `--models` | `~/.config/replichai/models.yaml` | the model catalogue |
| `--session-root` | `~/.replichai/sessions` | where session directories are made |
| `--retention` | `keep` | `keep`, `on-failure`, or `discard` |
| `--engine` | podman, then docker | with `--sandbox container` |
| `--image` | the published harness image | with `--sandbox container` |
| `--parallel` | half the cores, capped at 4 | with `replichai run`: papers at once |
| `--resume` | off | with `replichai run`: skip what finished |

Every flag has an environment variable: `REPLICHAI_SANDBOX`,
`REPLICHAI_HARNESS`, `REPLICHAI_MODELS_FILE`, `REPLICHAI_SESSION_ROOT`,
`REPLICHAI_SESSION_RETENTION`, `REPLICHAI_CONTAINER_ENGINE`,
`REPLICHAI_HARNESS_IMAGE`. Set them once for a machine.

How it is put together, and why, is in
[`architecture.md`](architecture.md); the design record is
[`specs/045`](../specs/045-the-run-happens-on-your-machine.md).

# Running audits on your own machine

`replichai audit` normally sends the work to a hosted agent backend
(Anthropic's Managed Agents, the `anthropic-managed` runtime). With
`--runtime local` it runs on your machine instead: the coding agent you already
have installed, confined to one folder, on the model you choose.

Everything else is the same: the same method (the agent's instructions in
[`method/`](../method/)), the same rubric, the same seven output files, the
same verdict. What changes is who owns the machine and whose model credential
is used. Nothing is charged to a replichai account, because you bought the
compute from the model vendor yourself.

```bash
replichai audit https://arxiv.org/abs/2603.01795 --runtime local
```

## What you need

One coding agent and one sandbox.

- The **harness** is the coding agent's command-line program that runs inside
  the sandbox and does the work: Claude Code (`claude`, the default) or Codex
  (`codex`). replichai starts it, feeds it the task, and reads its output
  stream; the harness's own login is what pays for the model unless you
  configure another endpoint.
- **srt** is Anthropic's sandbox runtime, the npm package
  `@anthropic-ai/sandbox-runtime`. It confines a process to the files and
  network hosts it is allowed, using Seatbelt on macOS and bubblewrap with
  seccomp on Linux, without a container or a virtual machine.

On a machine where `claude login` has already been run you need no API key and
no configuration file.

**macOS**

```bash
npm install -g @anthropic-ai/claude-code       # the agent
claude login
npm install -g @anthropic-ai/sandbox-runtime   # srt, the sandbox
brew install ripgrep                           # srt uses it to find files to deny
```

**Linux**

```bash
npm install -g @anthropic-ai/claude-code
claude login
npm install -g @anthropic-ai/sandbox-runtime
sudo apt-get install -y bubblewrap socat ripgrep    # or dnf, on Fedora and RHEL
```

**Windows** is not supported. Run replichai inside WSL2, where both tools work.

If anything is missing, the run refuses before it fetches the paper and prints
the commands that would fix it on your platform. Check what it found with:

```bash
srt echo ok
claude --version
```

## What a run does

Four steps, in order. Each is a separate process the runtime starts, watches
and can stop, so a failure says which step failed.

```
bootstrap   installs what the method declared into the session's own
            environment at $VIRTUAL_ENV, never into the workspace
worker      the agent: reads the paper, hunts for artifacts, writes and runs
            the reproduction, commits as it goes
judge       the same agent again, in a fresh sandbox, with the workspace
            read-only and the rubric as its task
push        your machine, after the sandbox is gone: verify what was
            committed, create the repository, push it
```

The judge cannot edit what it grades: the workspace is mounted read-only, and
the only place it may write is a directory the worker never sees. That is a
filesystem permission, not an instruction the model is asked to follow.

Before the judge starts, replichai itself reads the run's `verdict.json`
against its schema. The judge is told what it found, and a revision is given the
same list. A round whose findings replichai cannot read does not pass, whatever
the judge wrote: the run is revised, or fails once it has no revision left.
With `--max-revisions 0` a run is graded once and never revised.

## Where everything lands

Each session gets a directory under `~/.replichai/sessions/<id>/`:

| | |
|---|---|
| `workspace/` | your reproduction, and a git repository. This is what gets published |
| `tmp/` | downloads: the paper PDF, datasets, anything third-party. `$TMPDIR` points here, and so does the agent's own scratch space, so nothing either of them creates lands in the repository |
| `venv/` | the language environment the bootstrap made. The agent is told to use it, and in practice sometimes installs a second one inside `workspace/` anyway. Either way a dependency tree is never committed, never harvested into your output directory, and refused at publication |
| `inputs/` | what the run was handed to read, such as the study's copy of the paper. Every stage reads it and none can write it, and it is never part of the repository |
| `grade/` | the judge's verdict, one file per grading round |
| `session/` | the transcript, the status, and what the run ran on. Nothing inside the sandbox can write here |

`session/rounds/` keeps, for every grading round, `verdict.json` as the round
left it and what replichai found when it read it. When the run is published they
are committed beside the grades, as `grade/findings-<n>.json` and
`grade/checks-<n>.json`. Round 0 is what the run produced before any revision.

This split is what keeps the output clean. The agent works in `workspace/`, so
nothing else it needs can end up in the repository; the transcript is written
by replichai from a stream the agent cannot reach, so the run cannot edit its
own evidence.

`workspace/` is both the build directory and the deliverable, so replichai
keeps installed dependencies out of it by rules the agent does not control: the
repository ignores them, the harvest skips them, and publication refuses a tree
that carries one. A virtualenv is recognized by the `pyvenv.cfg` file Python
writes, so one under any name is caught.

Directories are kept after a run, because the workspace is the reproduction:

```bash
replichai sessions ls
replichai sessions prune --older-than 168h --dry-run
```

Set `REPLICHAI_SESSION_RETENTION=on-failure` or `discard` if you would rather
they were removed automatically.

## What the agent can and cannot reach

Inside the sandbox it can write to its own session directory and nothing else,
read the system toolchain, and reach the network only where the policy allows.

It does not see your shell's environment. A stage starts from a short, fixed
allowlist (the path, the locale, the time zone, the temporary directory, the
TLS certificate variables) plus what the run itself sets. An API key, a cloud
credential or a GitHub token exported in the terminal you launched from is not
passed in, and a key you did not choose for the run cannot override the login
you did choose.

Your home directory is denied, with named exceptions:

| Allowed | Why |
|---|---|
| your harness login, and nothing around it: `~/.claude.json` for Claude Code, read-only, or `~/.codex/auth.json` for codex, writable so a refreshed token is saved | it is your existing login. It is granted only to a session actually running on that login, never to one you handed an API key, and the grant is written into the run's environment record as `config_read_allowed` |
| `~/Library/Keychains`, read-only, macOS only | Claude Code keeps its credential there rather than in a file (on Linux, `~/.claude/.credentials.json` instead), and only a session actually using that login is granted it |
| `/tmp/codex-<your user id>`, for codex only | codex keeps a scratch area there |
| `~/.codex/packages`, read-only, for codex only | codex's own installer puts the program there |

The rest of `~/.claude` and `~/.codex` is denied, so a run cannot read your
own conversation history there (`projects/` and `history.jsonl`, codex's
`sessions/`, `archived_sessions/`, memories and logs). Codex writes its own
state into a configuration folder inside the run, with your token linked in.
`~/.claude.json` is your account file: it holds your account and the folders
you used Claude Code in, with their settings, but no conversation. A session
that must not see it should carry its own API key instead of your login.

Explicitly denied, and tested: `~/.ssh`, `~/.aws`, `~/.config/gcloud`,
`~/.kube`, `~/.netrc`, `~/.gnupg`, `~/.docker/config.json`,
`~/.git-credentials`, `~/.config/replichai`, and any `.env`. So is
`~/.codex/shell_snapshots`: your own codex records there every variable your
shell exports, credentials included, and codex in a run is told not to take a
snapshot of its own.

Runs share no temporary folder. The sandbox would otherwise hand every run the
same one, `/tmp/claude`, and one run would find what another downloaded or
built there. Each run keeps its temporary files in its own `tmp/` instead, and
`/tmp/claude` is denied to it.

Also denied, for a different reason: **your own standing instructions and
plugins.** `~/.claude/CLAUDE.md`, `~/.claude/settings.json`,
`~/.claude/plugins`, `~/.claude/agents`, `~/.claude/commands`,
`~/.claude/skills` and `~/.codex/AGENTS.md` are withheld from every run. They
are not part of the method, and a reproduction that silently inherits the
habits of the person who ran it is not reproducing the paper. Codex reports the
denial as an error line in its transcript; that is cosmetic and the run
continues.

**Outside your home directory, reads are allowed.** srt's default is to permit
reads everywhere and restrict by denial, and this is that default with your
home denied. A run can read `/usr`, `/opt`, `/etc` and anything else outside
`$HOME`. If that matters for your machine, use `--sandbox container`.

**No GitHub token enters the sandbox.** The agent commits into a folder that
outlives it, and your machine pushes that folder afterwards. That is why
`--publish` works locally without giving an agent write access to anything.

**With `--project`, no GitHub token needs to be on your machine either.** A
project is a container for papers and sessions in the replichai web app, so
this path needs an account there:

```bash
replichai login
replichai audit <paper> --runtime local --publish --project <id>
```

The app allocates the repository, from the same registry a session started in
the app allocates from, and hands back a credential that can write to that one
repository and expires within the hour. Your machine pushes with it and returns
it when the run is finalized. Without `--project`, `--publish` uses
`REPLICHAI_GITHUB_TOKEN`, a token broad enough to create repositories in the
GitHub organization `--owner` names, so prefer the project path where you have
one.
The default owner, `open-hai`, is the organization the hosted service publishes
into; with your own token, set `--owner` or `REPLICHAI_GITHUB_OWNER` to an
organization you control.

Two things that credential cannot do: `--invite` needs permission to administer
the repository, so add the collaborator in the app instead; and a deployment
with no GitHub App configured cannot mint a credential at all, in which case
the terminal says which settings are missing and falls back to your own token.
Only a GitHub App can issue a token limited to one repository, which is why
this path needs one configured on the deployment.

The read allowance has one cost: an agent that reads its own credential file
puts the contents in its transcript. Everything published is swept for
credential-shaped text first, and the transcript says where something was
removed. This is a backstop, not a guarantee. Read a transcript before you make
a repository public.

## Where a run is allowed to go

srt is allow-only: it has no "everything" mode and refuses a wildcard. So a
local audit is given a **declared list** of the places an artifact hunt goes
(preprint servers, publishers, code hosts, data repositories, package indexes,
and the services that resolve an identifier) plus whatever endpoint your model
is behind. The list is in
[`internal/audit/domains.go`](../internal/audit/domains.go), and the effective
policy is recorded in each run's `environment.json`.

This has a real cost, and the record is how you see it: a paper whose only
artifact link is an author's personal university page is not reachable, and
the hunt records it as *not fetched* rather than as *not released*. A container
run has unrestricted egress and no such bound, which is one more reason the two
drivers are not interchangeable.

`replichai run` does not leave this to the sandbox. Every run of a project is
given a named **network profile**, so two runs of one study reach the same
places:

| Profile | Reaches |
|---|---|
| `artifacts` (the default) | the declared list above, and your model's endpoint |
| `open` | anywhere. Only `--sandbox container` can enforce it |

Name it with `network:` in the project file, for the whole file or for one
paper, or with `--network` for a project file that names none. A sandbox that
cannot enforce the profile refuses the run instead of running it under another.
Each run's `replication.json` names its profile in `run.network_profile`.

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
  vllm:                                   # a vLLM server you host, started with --api-key
    base_url: https://vllm.example.org/v1
    dialect: openai-responses
    key_env: VLLM_API_KEY
  local:                                  # something on this machine, such as Ollama
    base_url: http://127.0.0.1:11434/v1
    dialect: openai-chat                  # no key_env: no credential is sent

models:
  - {provider: anthropic, id: claude-opus-5,    gateway: harness-login, harness: claude-code, preferences: [best]}
  - {provider: anthropic, id: claude-sonnet-5,  gateway: harness-login, harness: claude-code, preferences: [balanced]}
  - {provider: qwen,      id: qwen3-coder-480b, gateway: vllm,          harness: codex,       preferences: [best]}
  - {provider: meta,      id: llama-4-scout,    gateway: local,         harness: codex,       preferences: [fast]}
```

A **gateway** is an endpoint a model is reached through, and `dialect` is the
wire protocol it speaks: `anthropic` (Anthropic's Messages API),
`openai-responses` (OpenAI's Responses API) or `openai-chat` (OpenAI's Chat
Completions API). Any OpenAI-compatible endpoint works the same way, hosted
(OpenRouter, for example) or run by you (vLLM, Ollama, llama.cpp); declare
whichever dialect it implements. `id` is sent to the endpoint as the model
name, so it must be the name that endpoint serves: the vLLM row above assumes
the server was started with `--served-model-name qwen3-coder-480b`. `key_env`
names the environment variable that holds the endpoint's key; the key itself
never goes in the file. `preferences` lists the tiers (`best`, `balanced`,
`fast`) the row answers for that harness.

Then pick one per run:

```bash
replichai audit <paper> --runtime local --model best
replichai audit <paper> --runtime local --model qwen/qwen3-coder-480b --harness codex
```

The file is checked against these rules when the run starts, so a mistake
costs you an error message rather than forty seconds and a 400:

- **Dialect and harness must agree.** Claude Code speaks the Anthropic dialect;
  Codex speaks the OpenAI ones. An open-weights model behind an
  OpenAI-compatible gateway therefore pairs with `codex`.
- **One model per tier per harness.** Two `best` rows is an error, not a random
  choice: what a session ran on has to be determinate.
- **A named credential must exist.** A gateway naming `VLLM_API_KEY` with
  nothing in it fails now rather than mid-run.
- **An exact `--model provider/id` must be in the file.** A model you never
  declared has no endpoint to reach it through.

Your credentials are resolved once, passed to the agent without appearing in
the process table, and removed from anything published.

## A pinned toolchain instead

`--sandbox container` runs the same session inside an OCI container (Podman
first, then Docker) for the one thing your own machine cannot give: the same
Python and the same packages on every operator's machine. Use it for CI, or
when the point is to compare your reproduction against somebody else's.

```bash
replichai audit <paper> --runtime local --sandbox container --image <ref>
```

It costs an image pull, a virtual machine on macOS, and an API key instead of
your existing login. It also cannot filter the network by domain, which `srt`
can, so `--sandbox host` is the default. No harness image is published, so a
container run needs `--image` (or `REPLICHAI_HARNESS_IMAGE`) naming one you
built yourself, and without it the run is refused before anything starts; the
host sandbox needs nothing of the kind.

Every run of either kind writes `environment.json` beside the transcript: the
operating system, the sandbox, the agent and its version, the model, and the
package versions actually resolved. Two reproductions that disagree can then be
told apart from two that only ran on different interpreters.

## A whole project at once

```bash
replichai run papers.yaml --parallel 4 --publish
```

`papers.yaml` is one paper per line, or a list with per-paper overrides:

```yaml
project: chi-2026-audit
model: best
harness: claude-code
network: artifacts
publish:
  owner: open-hai
papers:
  - https://arxiv.org/abs/2603.01795
  - doi: 10.1145/3654777.3676417
    model: fast
    runs: 3
    file: papers/uist-2024.pdf
    artifacts:
      - https://github.com/authors/system
      - https://zenodo.org/records/1234567/files/study-data.zip
```

`publish.owner` is the GitHub organization the repositories are created in.
`open-hai` is the one the hosted service uses, and your token cannot create
repositories there, so put an organization you control in your own file (or
pass `--owner`).

Each paper gets its own session, its own sandbox and its own repository.
`runs: 3` reproduces a paper three times, each run in its own session with its
own repository; put `runs` at the top of the file to set it for every paper. One
paper failing never stops the rest; the exit code is non-zero if any failed, so
CI reads the exit code and you read the report.

**Every run of a paper reads the same copy of it.** Before any run starts,
replichai fetches each paper once (arXiv's PDF, the PDF the paper's page
declares, or an open-access copy) and hands that copy to every run, read-only
and outside the repository. Runs that each found their own copy could compare
their results against different versions of the paper. Many publishers refuse
automated clients, so a paper can also name its copy with `file:`, a path
relative to the project file. If a paper has no copy, the project stops before
anything runs and names the paper. The copies are kept in `papers/` beside the
project file and listed in `papers.json`, and each run's `replication.json`
cites its copy by digest in `run.paper_digest`.

**Every run of a paper reproduces from the same released materials.** List
what the authors released under `artifacts:`, and each is snapshotted once
before any run starts. A repository (on GitHub, GitLab, Bitbucket or Codeberg,
or any address ending in `.git`) is kept as a git bundle with the commit its
default branch pointed at; the run clones the bundle and works from the clone.
Anything else is kept as the file its address serves, so give the direct
download address: an address that serves a web page, such as a data
repository's landing page, is refused. A run can be handed up to 64 MB per
snapshot. The snapshots sit beside the paper's copy, and each run's
`replication.json` lists them in `run.artifacts` with their source, commit and
digest. The run still hunts for anything else the authors released and scores
it, as any run does.

Two files are written beside the project file and kept current as it runs:
`batch.json`, the state, and `report.md`, the table of what passed, what it
cost, and what did not happen. Interrupt it with Ctrl-C and nothing is lost:

```bash
replichai run papers.yaml --resume
```

Resume skips what finished and retries what failed, into the repository the
first attempt created. A retry whose recorded repository no longer exists is
refused rather than given a new one: edit `repo` in `batch.json`, or run
without `--resume`. A retry that fails early keeps the repository on record.
A resumed project hands its remaining runs the copies and snapshots its first
runs read, and refuses to start if one of them, a file the project names, or
the list of artifacts has changed since.

## Putting the results in the app

Optional. A local run needs no account and no network beyond your model
endpoint. If you do want the sessions in the replichai web app (the hosted one
at `replichai.latere.ai`, or a deployment of your own):

```bash
replichai login                              # device-code sign-in, at auth.latere.ai by default
replichai project create "CHI 2026 audit"
replichai project ls
replichai push papers.yaml --project <id>
```

Nothing about an imported session is billed, and none of it is charged to a
project's credits: you paid the vendor. The record carries the paper, the
model, the usage, the verdict and the repository; the evidence stays in the
repository the run pushed, which is why a run you intend to file should use
`--publish`.

Pushing twice is safe. A session updates its row rather than opening a second
one, so a failed upload is retried by running the command again.

These commands talk to the hosted app unless told otherwise. For a deployment
of your own, set `REPLICHAI_AUTH_URL` and `REPLICHAI_API_URL` first; what the
terminal's sign-in needs from that deployment's identity provider is in
[`self-hosting.md`](self-hosting.md#the-terminals-sign-in).

## Codex

`--harness codex` runs the same session through Codex instead. It needs
different things of the machine, and the code declares them rather than
assuming them:

- Its login is one file, `~/.codex/auth.json`, so no keychain is involved.
- It keeps its configuration and session records in a folder inside the run,
  with that file linked in. Only the token file is writable, so a token codex
  refreshes during the run is saved where your own codex reads it.
- It refuses a directory it has not been told to trust, and a session's
  workspace is a repository it has never seen, so the trust check is skipped.
  The sandbox is the boundary; Codex's own prompt is not.
- It is a Rust binary and will not trust the sandbox's certificate authority,
  so its endpoints are tunneled rather than inspected.

`~/.codex/AGENTS.md` is withheld like any standing instruction, and Codex
reports that as an error line in its transcript. The run continues.

## When something goes wrong

**"the host sandbox is not ready"**: the message names what is missing and the
install command for your platform. It also offers `--sandbox container`, which
needs none of it but does need a harness image you built (see above).

**"no container engine is installed" / "podman machine is not running"**: on
macOS Podman needs a virtual machine: `podman machine init --cpus 4 --memory
8192 --disk-size 100 && podman machine start`.

**"harness ... is unavailable"**: the name is `claude-code` or `codex`, and
the message lists what is installed.

**The push failed**: the workspace is kept and the run prints a `finish`
command that pushes it. Nothing is lost; the work is on disk.

**`pip install` fails with a certificate error**: pip verifies through the
operating system's trust service, which the sandbox does not open, so every
certificate fails for pip while curl and Python's own client succeed on the
same connection. The bootstrap passes `--trusted-host` for the indexes it
installs from, which moves the verification rather than dropping it: the
sandbox terminates TLS to those hosts, so it validates the real index
certificate itself before handing pip a locally-signed connection. An index of
your own needs the same treatment.

**The run committed nothing**: that is recorded rather than reported as
success. An empty repository is a real outcome, and the transcript in
`session/events.jsonl` says what the agent did instead.

## What is not there yet

- **Evidence is not uploaded to the app.** A session pushed without
  `--publish` arrives as a verdict with nothing behind it, because the evidence
  lives in the repository the run pushed.
- **The harness image is not published.** `--sandbox container` is built, but
  it runs only an image you build yourself and name with `--image`; without
  one the host sandbox is the only one you can run.
- **The app does not label an imported session** as locally executed, though it
  stores the fact.
- **`environment.json` records where a run was allowed to go, not where it
  went.** Nothing reads the sandbox's proxy log.
- **Reads outside your home directory are not restricted.** That is srt's
  default posture with your home denied on top; tightening it to an allowlist
  of the toolchain is a separate piece of work.

## Reference

| Flag | Default | What it chooses |
|---|---|---|
| `--runtime local` | `anthropic-managed` for `audit`, `local` for `run` | run the session on this machine |
| `--sandbox` | `host` | `host` (srt) or `container` (podman, docker) |
| `--harness` | `claude-code` | `claude-code` or `codex` |
| `--model` | `best` | with `replichai audit`: a tier (`best`, `balanced`, `fast`) or `provider/id`; a project file sets it per paper |
| `--models` | `~/.config/replichai/models.yaml` | the model catalog |
| `--session-root` | `~/.replichai/sessions` | where session directories are made |
| `--retention` | `keep` | `keep`, `on-failure`, or `discard` |
| `--engine` | podman, then docker | with `--sandbox container` |
| `--image` | none; required, since no harness image is published | with `--sandbox container` |
| `--parallel` | half the cores, capped at 4 | with `replichai run`: papers at once |
| `--resume` | off | with `replichai run`: skip what finished |
| `--network` | `artifacts` | with `replichai run`: the network profile for a project file that names none |
| `--max-revisions` | 3 | how many times a run may be revised after a grade; 0 grades it once and never revises it |

Most of these have an environment variable: `REPLICHAI_RUNTIME`,
`REPLICHAI_SANDBOX`, `REPLICHAI_HARNESS`, `REPLICHAI_MODELS_FILE`,
`REPLICHAI_SESSION_ROOT`, `REPLICHAI_SESSION_RETENTION`,
`REPLICHAI_CONTAINER_ENGINE`, `REPLICHAI_HARNESS_IMAGE`, and
`REPLICHAI_GITHUB_API_URL` for a GitHub Enterprise API root. Set them once for
a machine. A flag given on the command line wins over the variable.

How it is put together, and why, is in
[`architecture.md`](architecture.md).

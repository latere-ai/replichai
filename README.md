# replichai

Audits the reproducibility of research papers.

Give it a paper. It finds what the authors released, works out which parts of
the work a machine can re-run and which parts need people, implements and runs
the machine-runnable part, records every decision the paper left unwritten, and
returns a verdict: a per-component table saying what was reproduced, what only
partly, and what was blocked, each with its evidence or its specific blocker.

Built for research with human participants: human-computer interaction,
psychology, social science, and any other field where a finding rests on what
people did. A paper of that kind almost always contains two loops:

- an **inner loop**: the algorithm, model, system, or analysis pipeline, which
  a machine can re-run;
- an **outer loop**: the study that validates it with human participants.

The inner loop is reproducible. The outer loop is not, and reproducibility
claims most often go wrong by treating it as if it were. The audit places that
boundary precisely and defends it.

## Two ways in

Both run on the same engine, the same method, and the same rubric.

**The web application**: sign in, put papers in a project, start a replication
session per paper, watch it stream, read the verdict, take the repository out,
and pay for the compute it used. It runs at `replichai.latere.ai`.

**The command line**: one audit, one paper, no account and no database; or a
whole project of papers at once. It runs on a hosted agent backend, or on your
own machine with the coding agent you already have.

## Run an audit

You need Go 1.27 or newer.

```bash
go build ./cmd/replichai

export ANTHROPIC_API_KEY=...             # never commit this
replichai audit https://arxiv.org/abs/2603.01795 --out runs
```

A paper reference is a DOI, an arXiv URL or bare ID, a publisher URL, or a PDF
URL. The audit writes a reproduction repository into `runs/<paper>/`:

| File | Contents |
|---|---|
| `README.md` | What the paper is, what the reproduction is, how to run it |
| `REPRODUCIBILITY.md` | The verdict and the per-component reproduction table first, then the inner/outer boundary, the hidden decisions, and the open-science scorecard |
| `src/` | A best-effort implementation of the inner loop, runnable |
| `UNVERIFIED.md` | Everything that could not be confirmed, each with its specific blocker |
| `SOURCES.md` | The paper's identity and every artifact search performed, with its result |
| `verdict.json` | The same findings as data: boundary rows, hidden decisions, scorecard, what was released |
| `instrument.json` | The declared study protocol, the analysis entrypoint, and whether the outer loop could be hosted |

If `verdict.json` and `REPRODUCIBILITY.md` disagree, the JSON is authoritative;
the web application renders a session's findings from it. Any overall
reproduction rate is a derived footnote to the table, never a figure to compare
across papers, because how many components a paper decomposes into is itself a
judgment. [`docs/procedure.md`](docs/procedure.md) explains why.

## Run it on your own machine

`--runtime local` runs the whole session on your machine instead: the coding
agent you already have installed, confined to one folder, on the model you
choose. The method, rubric, and output are the same, and nobody bills you,
because you bought the compute from the vendor yourself.

```bash
npm install -g @anthropic-ai/sandbox-runtime   # the sandbox
brew install ripgrep                           # it uses this; Linux also needs bubblewrap and socat
replichai audit https://arxiv.org/abs/2603.01795 --runtime local
```

On a machine where `claude login` has already been run, this needs no API key
and no configuration. `--harness codex` runs the same session through Codex,
and a gateway in `models.yaml` runs an open-weights model. `--sandbox
container` runs it in Podman or Docker instead. No harness image is published
for it, so it needs `--image` naming one you built, and refuses to start
without it.

A whole project at once, with the results filed in the app:

```bash
replichai run papers.yaml --parallel 4 --publish --resume
replichai login && replichai push papers.yaml --project <id>
```

No GitHub token enters the sandbox: the agent commits into a folder that
outlives it, and your machine verifies and pushes that folder afterwards.

[`docs/local-runs.md`](docs/local-runs.md) is the full guide: prerequisites per
platform, what a run does, where files land, what the agent can and cannot
reach, choosing a model, and what to do when something refuses to run.

## Publish a reproduction

With `--publish`, the reproduction gets its own GitHub repository and the agent
commits into it as it works, so the history is the agent's own work rather than
one squashed drop:

```bash
export REPLICHAI_GITHUB_TOKEN=...       # can create and push repositories under the owner
replichai audit https://arxiv.org/abs/2603.01795 --publish --invite <github-login>
```

The repository is created private under `open-hai` (override with `--owner` or
`REPLICHAI_GITHUB_OWNER`) and named from the paper's identity, so repeat
reproductions of one paper are related rather than colliding. When the run
ends, the system, not the agent, verifies what was committed, writes
`replication.json`, commits the run's transcript as `transcript.jsonl` and
`TRANSCRIPT.md`, and invites the collaborator. With `--project <id>` after
`replichai login`, the app allocates the repository and hands the run a
credential for that one repository, so no GitHub token needs to be on your
machine at all. That credential can push but not administer, so `--invite` is
refused with `--project`; add the collaborator in the app instead.

Long runs do not need to hold the process open. `--detach` starts the run and
prints the command that finishes it later:

```bash
replichai audit <paper> --publish --detach
replichai finish open-hai/<repo> <paper> --session-ref <opaque-ref> --invite <github-login>
```

In the app, a session started from a project gets its own repository the same
way, and making a project public is a deliberate act by its owner with a
recorded sign-off.

## Commands

| Command | What it does |
|---|---|
| `replichai audit <paper>` | one audit: resolve the paper, run the session, write the reproduction; `--publish` gives it a repository |
| `replichai run <project-file>` | a whole project: many papers, one session each, in parallel and resumable |
| `replichai resolve <paper>` | print the identity and slug a reference resolves to; nothing else runs |
| `replichai publish <artifact-dir> <paper>` | the offline publish path: preview what a filesystem forge would write; write it with `--live` |
| `replichai finish <owner>/<repo> <paper>` | reattach to a detached run and finalize it: verify, write the manifest, commit the transcript, invite |
| `replichai providers` | the runtimes this build knows |
| `replichai rates` | check that the rate card names the models a runtime resolves to, from the compiled-in card or a live one (`--database-url`) |
| `replichai login` / `whoami` / `logout` | sign in to the app from the terminal by device code, show who you are, forget the credential |
| `replichai org [<org-uuid>\|--personal]` | show or switch the organization projects are filed under |
| `replichai sessions ls` / `replichai sessions prune` | list the session directories on this machine, and reclaim their space |
| `replichai project create <title>` / `replichai project ls` | the projects in the app that locally run sessions are filed under |
| `replichai project sessions <id>` / `replichai project rm-session <id>...` | list a project's sessions, and delete one |
| `replichai push <project-file> --project <id>` | file the sessions this machine ran under a project in the app |
| `replichai help` | this list; `replichai <command> -h` lists a command's flags with their defaults |

`--runtime` picks the runtime for one command, `REPLICHAI_RUNTIME` picks it for
every command in a shell, and with neither set `audit` uses `anthropic-managed`
and `run` uses `local`. The flag overrides the variable and the variable
overrides the default. The choices are `memory` (a free, deterministic fake),
`anthropic-managed`, and `local`. Each runtime reads only the variables named
for it, so a key can never reach a runtime it was not meant for.
[`example.env`](example.env) lists every variable the CLI and the server read.

## The method

What an audit does is not in the code. `method/` holds the worker's standing
instructions, the rubric every run is graded against, and the task template,
and their digest is recorded with every published reproduction, so two audits
can be compared only when they ran the same method. The grader runs inside the
loop: a run that falls short of the rubric revises and is graded again, up to
a budget.

[`docs/procedure.md`](docs/procedure.md) walks through one audit step by step:
acquiring the paper, the artifact hunt, splitting the inner loop from the outer
loop, the hidden decisions, the verdict, and the grading.

## Documentation

| Page | For |
|---|---|
| [`docs/local-runs.md`](docs/local-runs.md) | running audits on your own machine, alone or a whole project at once |
| [`docs/procedure.md`](docs/procedure.md) | what one audit does and why, for anyone reading or relying on a verdict |
| [`method/`](method/) | the method itself: instructions, rubric, task template |
| [`example.env`](example.env) | every setting the server and the CLI read, with its default |
| [`deploy/README.md`](deploy/README.md) | how the hosted deployment is configured and released |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | building, testing, and changing the code or the method |
| [`docs/architecture.md`](docs/architecture.md) | how the engine and the application are put together |

[`docs/README.md`](docs/README.md) is the full index.

## Run the web application locally

```bash
make up          # Postgres + the server, on http://localhost:8080
```

That brings up the whole stack in the foreground on the `memory` runtime, so it
costs nothing and needs no API key: a submitted paper moves from `submitted` to
`done` through the real HTTP, worker, and Postgres path with a fake audit
inside it. Sign-in is bypassed on loopback; a deployment runs behind OIDC.
`make down` removes the stack and its volume. [`CONTRIBUTING.md`](CONTRIBUTING.md)
covers rebuilding the frontend, the test suites, and the quality gate.

## License

[GNU Affero General Public License v3.0 or later](LICENSE).

# replichai

Audits the reproducibility of research papers.

Give it a paper. It finds what the authors actually released, works out which parts of the work a machine can re-run and which parts need people, implements and runs the machine-runnable part, records every decision the paper left unwritten, and returns a verdict with a reproducibility rate you can decompose.

Built for human-computer interaction research, where a paper almost always contains two loops:

- an **inner loop** — the algorithm, model, system, or analysis pipeline, mechanically re-runnable;
- an **outer loop** — the study that validates it with human participants.

The inner loop is reproducible. The outer loop is not, and pretending otherwise is the most common way reproducibility claims go wrong. Placing that boundary precisely, and defending it, is the point.

## Install

```bash
go build ./cmd/replichai
```

## Use

```bash
export REPLICHAI_API_KEY=...            # never commit this
replichai audit https://arxiv.org/abs/2603.01795 --out runs
replichai resolve 10.1145/3654777.3676417   # what identity and slug a reference gets
replichai providers
```

`REPLICHAI_API_KEY` holds the backend's ordinary API key. The name is neutral so
the variable does not advertise which backend is behind it, not because the
value is different. If you already have that backend's own environment variable
exported, or are logged in to its CLI, you can leave `REPLICHAI_API_KEY` unset
and the existing credential is used — there is no need to copy the same secret
under a second name.

The audit writes a reproduction repository into `runs/<paper>/`:

| File | Contents |
|---|---|
| `README.md` | What the paper is, what the reproduction is, how to run it |
| `REPRODUCIBILITY.md` | Verdict and rate, the inner/outer boundary table, the hidden decisions, the open-science scorecard |
| `src/` | A best-effort implementation of the inner loop, runnable |
| `UNVERIFIED.md` | Everything that could not be confirmed, each with its specific blocker |
| `SOURCES.md` | The paper's identity and every artifact search performed, with its result |

Reproduction output is never committed to this repository. See [`docs/architecture.md`](docs/architecture.md).

## Publish a reproduction

With `--publish`, the reproduction gets its own GitHub repository and the agent
commits into it live, so the history is the agent's own work rather than one
squashed drop:

```bash
export REPLICHAI_GITHUB_TOKEN=...       # server-side; never enters the sandbox unscoped
replichai audit https://arxiv.org/abs/2603.01795 --publish --invite <github-login>
```

The repository is created private under `open-hai` (override with `--owner` or
`REPLICHAI_GITHUB_OWNER`), named from the paper's identity so repeat
reproductions of one paper are related rather than colliding. When the run ends,
the system — not the agent — verifies what was committed, writes
`replication.json`, commits `transcript.jsonl` and `TRANSCRIPT.md`, and invites
the collaborator.

Long runs need not hold the process open. `--detach` starts the run and prints
the command that finishes it later:

```bash
replichai audit <paper> --publish --detach
replichai finish open-hai/<repo> <paper> --session <id> --invite <github-login>
```

`replichai publish <artifact-dir> <paper>` is the offline path: it previews what
a filesystem forge would write, and writes it only with `--live`.

## Layout

```
cmd/replichai/       the CLI — argument parsing, output, and the composition root
internal/audit/      the orchestrator
internal/paper/      resolving a reference to a stable identity and a slug
internal/method/     loading the method from disk
internal/provider/   the backend contract, domain types, and adapters
internal/publish/    the manifest, the redistribution gate, the transcript, the forge seam
internal/registry/   the derived registry of published reproductions
internal/github/     the small GitHub client publishing needs
method/              the audit itself — instructions, rubric, task template (data, not code)
evals/               golden set: papers with known answers, used as a regression check
docs/                architecture and procedure
specs/               designs, and the record of what got built
reference/python/    the Python reference implementation of the core, checked against Go
```

## The method is data

`method/` holds three documents: the worker's standing instructions, the rubric every run is graded against, and the task template. Changing the rubric is a text edit with no rebuild and no redeployment, which is what makes iterating on the bar cheap. Both implementations read the same three files.

Whenever the method changes, re-run the golden set before trusting it:

```bash
replichai audit "$(head -1 evals/case-01/input.txt)" --out runs
```

Compare against `evals/case-01/expected.md`. The two cases are chosen to fail in opposite directions — one paper whose repository exists but is empty, one that genuinely practises open science — so a change that games either one shows up in the other.

## Development

```bash
go test ./... -cover
go vet ./... && gofmt -l .
```

Tests need no network and no credential: the backend adapter is exercised against a local HTTP server, and an in-process provider stands in for the backend everywhere else.

## License

Proprietary. All rights reserved.

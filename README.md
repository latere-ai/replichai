# replichai

Audits the reproducibility of research papers.

Give it a paper. It finds what the authors actually released, works out which parts of the work a machine can re-run and which parts need people, implements and runs the machine-runnable part, records every decision the paper left unwritten, and returns a verdict with a reproducibility rate you can decompose.

Built for research with human participants — human-computer interaction, psychology, social science, and anywhere else a finding rests on what people did. A paper of that kind almost always contains two loops:

- an **inner loop** — the algorithm, model, system, or analysis pipeline, mechanically re-runnable;
- an **outer loop** — the study that validates it with human participants.

The inner loop is reproducible. The outer loop is not, and pretending otherwise is the most common way reproducibility claims go wrong. Placing that boundary precisely, and defending it, is the point.

## Two ways in

The same engine runs behind both.

**The web application** is the product: sign in, put papers in a project, start a replication session per paper, watch it stream, read the verdict, take the repository out, and pay for the compute it burned. It is what runs at `replichai.latere.ai`.

**The CLI** is the engine with a terminal on it: one audit, one paper, no account and no database. It is how the method is exercised and how the golden set is re-run.

## Run the app locally

```bash
make up          # Postgres + the server, on http://localhost:8080
```

That brings up the whole stack in the foreground. It defaults to the in-process `memory` runtime, so it costs nothing and needs no API key: a submitted paper walks `submitted → done` through the real HTTP, worker, and Postgres path with a fake audit inside it. Sign-in is bypassed locally (`--dev`, refused off loopback); production runs behind OIDC.

The server embeds the frontend, so a code change in `frontend/` needs a rebuild:

```bash
cd frontend && bun install && bun run build   # writes internal/spa/dist
```

`make down` removes the stack and its volume.

## Run one audit from the CLI

```bash
go build ./cmd/replichai

export ANTHROPIC_API_KEY=...             # never commit this
replichai audit https://arxiv.org/abs/2603.01795 --out runs
replichai resolve 10.1145/3654777.3676417   # what identity and slug a reference gets
replichai rates                             # what a session costs, per model
replichai providers
```

`REPLICHAI_RUNTIME` selects the managed execution runtime. Today the choices are
`memory` and `anthropic-managed`; `PROVIDER=anthropic` and `REPLICHAI_API_KEY`
remain deprecated transition aliases. Runtime credentials are namespaced so a
future OpenAI runtime can never receive an Anthropic key accidentally.

The audit writes a reproduction repository into `runs/<paper>/`:

| File | Contents |
|---|---|
| `README.md` | What the paper is, what the reproduction is, how to run it |
| `REPRODUCIBILITY.md` | Verdict and rate, the inner/outer boundary table, the hidden decisions, the open-science scorecard |
| `src/` | A best-effort implementation of the inner loop, runnable |
| `UNVERIFIED.md` | Everything that could not be confirmed, each with its specific blocker |
| `SOURCES.md` | The paper's identity and every artifact search performed, with its result |
| `verdict.json` | The same findings as data — boundary rows, hidden decisions, scorecard, what was released |
| `instrument.json` | The declared study protocol, the analysis entrypoint, and whether the outer loop could be hosted |

The two JSON files are what the app reads to render a verdict without anyone opening a markdown file.

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
replichai finish open-hai/<repo> <paper> --session-ref <opaque-ref> --invite <github-login>
```

`replichai publish <artifact-dir> <paper>` is the offline path: it previews what
a filesystem forge would write, and writes it only with `--live`.

The app does the same thing without the terminal: a session started from a
project gets its own repository, and publishing a project is an owner's
deliberate act with a recorded sign-off.

## Layout

```
cmd/replichai/       the CLI — argument parsing, output, and the composition root
cmd/server/          the web app composition root — engine + stores + gate + worker + HTTP
internal/audit/      the orchestrator
internal/paper/      resolving a reference to a stable identity and a slug
internal/method/     loading the method from disk
internal/managed/    the vendor-neutral managed-session contract and controller
internal/runtimes/   explicit runtime catalog, durable router, and composition
internal/provider/   the transitional Anthropic protocol adapter behind managed
internal/publish/    the manifest, the redistribution gate, the transcript, the forge seam
internal/registry/   the derived registry of published reproductions
internal/github/     the small GitHub client publishing needs
internal/verdict/    the schema for verdict.json — the findings as data
internal/instrument/ the schema for instrument.json — the study protocol as data
internal/experiment/ running a reproduction's declared analysis on new data
internal/job/        the session lifecycle: states, the one transition function, the store, the worker
internal/project/    projects, the papers they hold, and the sessions run against them
internal/access/     who may sign in, who may operate, at what tier
internal/webauth/    OIDC login, the session cookie, the gate policy, the admin routes
internal/platform/   the policy row an admin tunes at runtime
internal/credit/     the ledger — wallets, project pots, holds and charges, in micro-USD
internal/payment/    the card-processor seam, and its Stripe adapter
internal/profile/    the researcher identity a public sign-off is published under
internal/notify/     what the platform says to somebody who is not looking at it
internal/draft/      one short model call, for drafting a project description
internal/httpapi/    the HTTP surface: submit, stream, read, publish, pay
internal/spa/        embeds and serves the built frontend
frontend/            the researcher UI — bun + React + Vite
method/              the audit itself — instructions, rubric, task template (data, not code)
evals/               golden set: papers with known answers, used as a regression check
deploy/              k8s kustomize for staging and prod
docs/                architecture, procedure, landscape
specs/               designs, and the record of what got built
```

## The method is data

`method/` holds three documents: the worker's standing instructions, the rubric every run is graded against, and the task template. Changing the rubric is a text edit with no rebuild and no redeployment, which is what makes iterating on the bar cheap.

Whenever the method changes, re-run the golden set before trusting it:

```bash
replichai audit "$(head -1 evals/case-01/input.txt)" --out runs
```

Compare against `evals/case-01/expected.md`. The two cases are chosen to fail in opposite directions — one paper whose repository exists but is empty, one that genuinely practises open science — so a change that games either one shows up in the other.

## Development

```bash
make test        # the whole Go suite, offline against fakes
make cover       # the number to quote — runs the Postgres contracts rather than skipping them
make e2e         # submit → run → done through the real server, no Docker
make web-e2e     # the frontend's browser regressions, in headless Chrome
make smoke       # the compose stack, end to end
go vet ./... && gofmt -l .
```

`make test` needs no network and no credential: the backend adapter is exercised against a local HTTP server, and an in-process provider stands in for the backend everywhere else. The store contracts that need a real Postgres skip themselves when `TEST_DATABASE_URL` is unset — [`CONTRIBUTING.md`](CONTRIBUTING.md) explains how to run them safely.

## License

Proprietary. All rights reserved.

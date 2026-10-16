# Contributing

This page is for people changing replichai's code or its method. How the
pieces fit together, and why each layer is split where it is, is in
[`docs/architecture.md`](docs/architecture.md); the design records are in
[`specs/`](specs/README.md).

## Getting set up

You need Go 1.27 or newer, [bun](https://bun.sh) for the frontend, and Docker
or Podman for the database tests and the local stack. Nothing else: no
account, no API key, and no access to anything Latere runs.

```bash
git clone https://github.com/latere-ai/replichai
cd replichai
make hooks       # the pre-commit and pre-push checks
make check       # every gate CI runs
make up          # Postgres + the server on http://localhost:8080, on the free memory runtime
```

A real audit needs a model: an Anthropic API key for the `anthropic-managed`
runtime, or a coding agent you are already logged in to for `--runtime
local`. [`docs/local-runs.md`](docs/local-runs.md) covers the second.

## Sending a change

Fork the repository, work on a branch, and open a pull request. Keep one
logical change per commit, stage the files explicitly, and write the subject
as `scope: what changed`, in lowercase, for whoever reads the log. Maintainers
push to `main` directly; CI runs the same gate on every push and pull
request, and a `v*` tag cuts a release. A change people will notice gets an
entry under `Unreleased` in [`CHANGELOG.md`](CHANGELOG.md), written for whoever
runs an audit or operates a deployment.

Sign off every commit in a pull request with `git commit -s`. The sign-off
certifies the [Developer Certificate of Origin](https://developercertificate.org/):
that you wrote the change, or otherwise have the right to submit it under the
project's license, [AGPL-3.0-or-later](LICENSE). A pull request whose commits
are not signed off is not merged.

If you are planning something large, open an issue first.

## Specs first

A feature starts as a spec in [`specs/`](specs/README.md), with acceptance
criteria that are testable sentences. The implementation follows the spec, and
a divergence is recorded in the spec's Outcome section rather than left in the
code. Names in a spec (commands, flags, routes, configuration variables, JSON
fields) are the names the code uses.

Small fixes do not need a spec. A change to the CLI, the HTTP API, a
configuration variable, a file an audit writes, or the method does.

## Where things are

```
cmd/replichai/       the CLI: argument parsing, output, and the composition root
cmd/server/          the web app composition root: engine + stores + gate + worker + HTTP
internal/audit/      the orchestrator
internal/paper/      resolving a reference to a stable identity and a slug
internal/fetch/      the one outbound fetcher for user-supplied URLs
internal/method/     loading the method from disk
internal/managed/legacy/  the anthropic-managed runtime over the provider protocol
internal/runtimes/   the runtime catalog, the durable router and the controller that
                     drives a session; builtin/ is the one place concrete runtimes
                     are composed
internal/runtimes/local/  the same engine on your own machine: sandbox drivers,
                     harness adapters, the model catalog
internal/provider/   the backend protocol adapter behind anthropic-managed, and an
                     in-process stand-in for tests
internal/batch/      one project: many papers, in parallel, resumable
internal/cloud/      signing in from the terminal, and the API client
internal/publish/    the manifest, the redistribution gate, the transcript, the forge seam
internal/registry/   the derived registry of published reproductions
internal/github/     the small GitHub client publishing needs
internal/verdict/    the schema for verdict.json: the findings as data
internal/instrument/ the schema for instrument.json: the study protocol as data
internal/experiment/ running a reproduction's declared analysis on new data
internal/job/        the session lifecycle: states, the one transition function, the store, the worker
internal/project/    projects, the papers they hold, and the sessions run against them
internal/access/     who may sign in, who may operate, at what tier
internal/webauth/    OIDC login, the session cookie, the gate policy, the admin routes
internal/platform/   the policy row an admin tunes at runtime
internal/credit/     the ledger: wallets, project pots, holds and charges, in micro-USD
internal/profile/    the researcher identity a public sign-off is published under
internal/notify/     outbound messages to people not using the app, such as a project invitation
internal/draft/      one short model call, for drafting a project description
internal/httpapi/    the HTTP surface: submit, stream, read, publish, pay (payments.go
                     holds the Stripe checkout and webhook routes)
internal/seo/        the head the server writes, the sitemap, and the addresses Go knows
internal/spa/        embeds and serves the built frontend
internal/doccheck/   tests that example.env, docs/cli.md, and docs/architecture.md match the code
internal/oteltest/   a recording tracer for tests that check the spans instrumentation produces
frontend/            the researcher UI: bun + React + Vite
method/              the audit itself: instructions, rubric, task template (data, not code)
evals/               the golden set: papers with known answers, used as a regression check
deploy/              Kubernetes manifests for the hosted deployment
docs/                the guides, the procedure, and the architecture
specs/               designs, and the record of what got built
```

## Checks

One command runs every check, and CI runs the same one:

```bash
make check                # go tool lateregate: every gate that applies, in order
go tool lateregate list   # the plan: which gates run, which are skipped, and why
make lint                 # one gate on its own: golangci-lint against the shared config
```

`make check` formats, vets and tests (plain, under the race detector, with only
the toolchain on PATH, and checking that nothing is left under `TMPDIR`),
lints, runs `govulncheck`, and checks the license headers, the spec tree, and
that every outbound HTTP client carries a tracing transport. Every gate lives
in lateregate, which is pinned as a tool in `go.mod` and runs golangci-lint
through `go run` at a pinned version, so nothing beyond the Go toolchain needs
installing.

`make lint-dead` is the one gate outside lateregate: `go tool deadcode -test
./...`. It fails on a function nothing reaches. The `-test` flag counts the
test binaries as roots, so code that only the suite exercises (a fake, a
fixture helper, a constructor a test wires) is reachable and passes. What fails
is code that no main, no test and no exported path can reach. Delete such code
rather than annotating it.

Install the hooks once per clone:

```bash
make hooks   # pre-commit: the cheap gates over the staged Go files;
             # pre-push: golangci-lint over the packages the push changes
```

Beyond the unit suite there are four targets, each covering something the
others cannot:

```bash
make cover-pg   # coverage with the Postgres contracts running rather than skipping
make e2e        # submit -> run -> done through the real server, pure Go
make web-e2e    # the frontend's browser regressions, in headless Chrome
make smoke      # the compose stack, end to end
```

`make cover-pg` is the coverage figure to quote. `go test -cover ./...` with no
database silently skips every Postgres store contract, which costs
`internal/access` alone about forty points and makes the total meaningless.

Tests need no network and no credential: the backend adapter runs against a
local HTTP server, and an in-process provider stands in for the backend
everywhere else. A test that needs either is in the wrong place. The one
deliberate exception is the opt-in end-to-end run described below.

## The frontend

`frontend/` is bun + React + Vite, and the server embeds its build, so a change
there is invisible to a running server until you rebuild:

```bash
cd frontend
bun install
bun run build          # vite writes ../internal/spa/dist, which the server embeds
bun test src           # the unit tests
bun run typecheck      # the app and, through tsconfig.test.json, the tests too
bun run test:browser   # every test/*.test.mjs in headless Chrome; skips cleanly with no Chrome
```

The browser tests are the only ones that can catch a layout bug, because layout
needs a layout engine. They render a screen against a mocked API and measure
it. This is how the mobile spill was found: a pane scrolled sideways while
`documentElement.scrollWidth` read clean. They share one harness
(`test/harness.mjs`: Chrome, one Vite server on a free port, a declarative
route table whose event stream answers with a snapshot frame first, as the
server does) and one runner (`test/run.mjs`) that runs every file and reports
every failure. Adding a test means adding a file. `node test/run.mjs <substring>`
runs a subset, and `JOBS=1` serializes them.

Every string the UI renders lives in `frontend/src/i18n.ts`; today the only
language is English. Adding a language means adding a code to the `Lang` union
and a matching block, and TypeScript then requires that block to translate
every key, so a locale cannot ship half-done. Dates, durations and amounts go
through `frontend/src/locale.ts` with the app's language, never the browser's;
a pure module or a test takes a translator from `translator(lang)`. The
coverage test in `i18n-coverage.test.ts` scans `.ts` and `.tsx` for a literal
phrase, a literal `aria-label`, or a `toLocale*String` call without the app's
language.

## The Postgres contracts

A few store contracts assert things only a real database can show: that
`SELECT ... FOR UPDATE SKIP LOCKED` hands disjoint work to competing claimants,
and that a project's credits stay exact under concurrent submits. They read
`TEST_DATABASE_URL` and skip when it is unset, so plain `go test ./...` stays
offline and green.

To run them, start the database and use the target:

```bash
docker compose up -d postgres   # podman compose works too; `make up` runs the
                                # whole stack in the foreground instead
make db-contract
```

**Point `TEST_DATABASE_URL` at `replichai_test`, never at `replichai`.** The
`job` and `access` contracts migrate and truncate whatever database they are
given, so pointing them at the dev database destroys the rows you were working
with, without warning. `make db-contract` pins the scratch database and creates
it on first use; the hazard exists only when you set the variable by hand:

```bash
# Wrong. Truncates your local access grants and project memberships;
# the job contract does the same to your sessions.
TEST_DATABASE_URL=postgres://replichai:replichai@localhost:5432/replichai \
    go test ./internal/access/
```

Two details to know if you run them yourself rather than through the target:

- `-p 1` is required. Go runs packages in parallel, and `job` and `access` both
  migrate and truncate the one database in the URL; if they share it they race
  into failures that look like real bugs.
- `platform`, `credit`, `project` and `profile` never touch that database's
  tables. Each creates a private database on the same server
  (`replichai_platform_test` and so on) and drops and recreates it per run, so
  they share a `-p 1` run safely; the URL's user only needs permission to
  `CREATE DATABASE`. `make db-contract` runs the first three beside `job` and
  `access`; `profile` has the same shape and runs with
  `make db-contract DB_CONTRACT_PKGS=./internal/profile/`.

## The real runtime end-to-end tests

`internal/runtimes/local` has two tests that drive the real tools.
`TestRealEndToEnd` and `TestRealEndToEndWithCodex` run one session through
`srt` confining a real Claude Code or Codex, on the login this machine already
holds: bootstrap, worker and judge, end to end. They launch a paid agent
session and take minutes, so they are opt-in: set `REPLICHAI_E2E=1` to run
them; without it they skip. `make test` never sets it, and neither does CI.

```bash
REPLICHAI_E2E=1 go test ./internal/runtimes/local/ -run TestRealEndToEnd -v
```

## What the linter is configured for

`.golangci.yml` is generated, not written by hand: `go tool lateregate golangci`
(also `make lint-config`) renders it from the shared config plus the
`golangci:` section of `.lateregate.yaml`, and the rendered file is gitignored.
Change the linting in `.lateregate.yaml`, never in the file it produces. That
section turns on correctness and clarity checks beyond the shared defaults, and
carries two deliberate relaxations, each with its reason beside it:

- `gosec` G301/G306 are off. A reproduction repository is meant to be read,
  committed, and browsed, so `0755` directories and `0644` files are correct
  there; the linter's defaults assume secrets handling.
- `errcheck` ignores writes to stdout and stderr. A failed write to the
  terminal is not actionable, because there is nowhere left to report it.

Everything is written in American English: identifiers, as the standard
library's `context.Canceled` is, and prose, meaning comments, documents, and
the UI's strings.

Every `//nolint` must name the specific check and say why; `nolintlint`
enforces this.

## Adding a runtime

A runtime is anything that can run one managed session: start it, report its
status, hand back its events and artifacts, cancel it, and release it. The
contract is the managed-session port, `latere.ai/x/managed-agents`, which
ReplicHAI imports rather than keeps a copy of. Adding a runtime is two small
jobs:

1. **The adapter**, in the port's repository as a module of its own (as
   `latere.ai/x/managed-agents/latere` is) or in a repository of its own. It
   implements `managed.Runtime`, declares honestly what the backend can do,
   and passes the port's conformance suite, `contracttest`. The port's adapter
   guide has the checklist.
2. **The entry**, in the catalog in `internal/runtimes/builtin`, the only
   package allowed to import a concrete runtime: its name, how it is built
   from configuration, and the variables `internal/runtimes/config.go` reads
   for it. Document the variables in `example.env` and the runtime in
   `docs/cli.md`.

There is no `init` registration: a runtime exists because the catalog names
it, and nothing above the contract changes.

## Changing the method

`method/` is data. Editing the rubric needs no rebuild, which keeps iterating
on the bar cheap, so re-run the golden set in `evals/` before trusting a
change:

```bash
replichai audit "$(head -1 evals/case-01/input.txt)" --out runs
```

and compare the result with `evals/case-01/expected.md`, then the same for
`case-02`. The two cases pull in opposite directions on purpose: one paper
whose repository exists but is empty, one that genuinely practices open
science. A change that games either tends to show up in the other.

Three of the four files in `method/` (the instructions, the rubric, and the
task template) are hashed into the method digest every published reproduction
records, so any edit to them, a typo fix included, makes later audits
incomparable with earlier ones. `experiment.md` is kept outside the digest, so
editing the experiment doctrine cannot change what an audit is comparable
against.

## Reporting a vulnerability

Do not open an issue. [`SECURITY.md`](SECURITY.md) says where to send it.

# Contributing

## Checks

One command runs every check, and CI runs the same one:

```bash
make check                # go tool lateregate: every gate that applies, in order
go tool lateregate list   # the plan: which gates run, which are skipped, and why
make lint                 # one gate on its own: golangci-lint against the shared config
```

`make check` formats, vets and tests (plain, under the race detector, with only
the toolchain on PATH, and checking that nothing is left under `TMPDIR`),
lints, runs `govulncheck`, and checks the licence headers, the spec tree, and
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
runs a subset, and `JOBS=1` serialises them.

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

Go identifiers follow the standard library's US spelling (`context.Canceled`).
Prose (comments, documents, the UI's strings) is British English, and
`misspell` is told in `.lateregate.yaml` which British forms to accept.
"Artifact" is the domain term and keeps its US spelling throughout.

Every `//nolint` must name the specific check and say why; `nolintlint`
enforces this.

## Adding a backend

Write a package under `internal/provider/` that satisfies `provider.Provider`
and registers itself from `init`. That package is the one place its backend's
SDK and vocabulary live, so nothing above the seam changes.

## Changing the method

`method/` is data. Editing the rubric needs no rebuild, which keeps iterating
on the bar cheap, so re-run the golden set in `evals/` before trusting a
change. The two cases pull in opposite directions on purpose: one paper whose
repository exists but is empty, one that genuinely practises open science. A
change that games either tends to show up in the other.

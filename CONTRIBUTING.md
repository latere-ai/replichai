# Contributing

## Checks

The bar is one command, and it is the one CI runs:

```bash
make check                # go tool lateregate: every gate that applies, in order
go tool lateregate list   # the plan — which gates run, which are skipped, and why
make lint                 # one gate on its own: golangci-lint against the shared config
```

`make check` formats, vets and tests (plain, under the race detector, with only
the toolchain on PATH, and watching that nothing is left under `TMPDIR`),
lints, runs `govulncheck`, and checks the licence headers, the spec tree, and
that every outbound HTTP client carries a tracing transport. Every gate lives
in lateregate, pinned as a tool in `go.mod`, and it runs golangci-lint itself
through `go run` at a pinned version — so beyond the Go toolchain there is
nothing to install.

`make lint-dead` is the one gate outside lateregate: `go tool deadcode -test
./...`. It fails on a function nothing reaches. The `-test` flag counts the
test binaries as roots, so a seam that only the suite exercises — a fake, a
fixture helper, a constructor a test wires — is reachable and passes; what
fails is code no main, no test and no exported path can arrive at. Delete it
rather than annotate it.

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

`make cover-pg` is the number to quote. `go test -cover ./...` with no database
silently skips every Postgres store contract, which costs `internal/access`
alone about forty points and makes the total meaningless.

Tests need no network and no credential: the backend adapter runs against a
local HTTP server, and an in-process provider stands in for the backend
everywhere else. A test that needs either is in the wrong place.

## The frontend

`frontend/` is bun + React + Vite, and the server embeds its build, so a change
there is invisible to a running server until you rebuild:

```bash
cd frontend
bun install
bun run build      # vite writes ../internal/spa/dist, which the server embeds
bun test src       # the unit tests
bun run typecheck
npm run test:browser   # the puppeteer regressions; skips cleanly with no Chrome
```

The browser tests are the only ones that can catch a layout bug, because layout
needs a layout engine. They render a screen against a mocked API and measure it
— which is how the mobile spill was found, in a pane that scrolled sideways
while `documentElement.scrollWidth` read clean.

Every string the UI renders lives in `frontend/src/i18n.ts` — English only
today. Adding a language is a code in the `Lang` union plus a matching
block, and TypeScript then requires that block to translate every key, so a
locale cannot ship half-done.

## The Postgres contracts

A few store contracts assert things only a real database can show — that
`SELECT ... FOR UPDATE SKIP LOCKED` hands disjoint work to competing claimants,
that a project's credits stay exact under concurrent submits. They read
`TEST_DATABASE_URL` and skip when it is unset, so plain `go test ./...` stays
offline and green.

To run them, start the database and use the target:

```bash
docker compose up -d postgres   # podman compose works too; `make up` runs the
                                # whole stack in the foreground instead
make db-contract
```

**Point `TEST_DATABASE_URL` at `replichai_test`, never at `replichai`.** The
`job`, `access`, and `platform` contracts migrate and truncate whatever database
they are handed, so aiming them at the dev database silently destroys the rows
you were working with. `make db-contract` pins the scratch database and creates
it on first use; the hazard is real only when you set the variable by hand:

```bash
# Wrong. Truncates your local access grants and project memberships;
# the job contract does the same to your sessions.
TEST_DATABASE_URL=postgres://replichai:replichai@localhost:5432/replichai \
    go test ./internal/access/
```

Two details worth knowing if you run them yourself rather than through the
target:

- `-p 1` is required. Go runs packages in parallel, and the `access` and
  `platform` contracts both truncate `access_grants`; sharing one database they
  race into failures that look like real bugs.
- The `project` and `profile` contracts are exempt from all of this. They cut
  their own scratch database from whatever URL you give them, which is why
  `make db-contract` leaves them out.

## What the linter is configured for

`.golangci.yml` is generated, not written: `go tool lateregate golangci` (also
`make lint-config`) renders it from the shared config plus the `golangci:`
section of `.lateregate.yaml`, and the rendered file is gitignored. Change the
linting in `.lateregate.yaml`, never in the file it produces. That section
turns on correctness and clarity checks beyond the shared defaults, and carries
two deliberate relaxations, each with its reason beside it:

- `gosec` G301/G306 are off. A reproduction repository is meant to be read,
  committed, and browsed, so `0755` directories and `0644` files are right
  here; the linter's defaults assume secrets handling.
- `errcheck` ignores writes to stdout and stderr. A failed write to the
  terminal is not actionable — there is nowhere left to report it.

Go identifiers follow the standard library's US spelling (`context.Canceled`).
Prose — comments, documents, the UI's strings — is British English, and
`misspell` is told in `.lateregate.yaml` which British forms to accept.
"Artifact" is the domain term and keeps its US spelling throughout.

Every `//nolint` must name the specific check and say why; `nolintlint`
enforces this.

## Adding a backend

Write a package under `internal/provider/` that satisfies `provider.Provider`
and registers itself from `init`. That package is the one place its backend's
SDK and vocabulary live, so nothing above the seam changes.

## Changing the method

`method/` is data. Editing the rubric needs no rebuild, which is what makes
iterating on the bar cheap — so re-run the golden set in `evals/` before
trusting a change. The two cases pull in opposite directions on purpose: one
paper whose repository exists but is empty, one that genuinely practises open
science. A change that games either tends to show up in the other.

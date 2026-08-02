# Contributing

## Checks

```bash
go build ./...
go test ./... -cover
go vet ./...
golangci-lint run ./...
```

All four must be clean. Install the linter with:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

Tests need no network and no credential: the backend adapter runs against a
local HTTP server, and an in-process provider stands in for the backend
everywhere else. A test that needs either is in the wrong place.

## The Postgres contracts

A few store contracts assert things only a real database can show — that
`SELECT ... FOR UPDATE SKIP LOCKED` hands disjoint work to competing claimants,
that the session quota stays exact under concurrent submits. They read
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

`.golangci.yml` turns on correctness and clarity checks beyond the defaults.
Two deliberate relaxations, both documented in the file:

- `gosec` G301/G306 are off. A reproduction repository is meant to be read,
  committed, and browsed, so `0755` directories and `0644` files are right
  here; the linter's defaults assume secrets handling.
- `errcheck` ignores writes to stdout and stderr. A failed write to the
  terminal is not actionable — there is nowhere left to report it.

Spelling is US English, matching the Go standard library (`context.Canceled`).
"Artifact" is the domain term and is spelled that way throughout.

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

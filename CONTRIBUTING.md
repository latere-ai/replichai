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
and registers itself from `init`. That package is the only place its backend's
vocabulary may appear — `TestOnlyTheAdapterNamesTheBackend` walks the
repository and fails otherwise. Nothing above the seam changes.

## Changing the method

`method/` is data. Editing the rubric needs no rebuild, which is what makes
iterating on the bar cheap — so re-run the golden set in `evals/` before
trusting a change. The two cases pull in opposite directions on purpose: one
paper whose repository exists but is empty, one that genuinely practises open
science. A change that games either tends to show up in the other.

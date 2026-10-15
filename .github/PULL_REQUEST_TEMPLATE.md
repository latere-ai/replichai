## What this changes

<!-- What is different after this, for whoever runs an audit, reads a verdict, operates a deployment, or builds on the code. -->

## Why

<!-- The problem it solves. Link the issue or the spec if there is one. -->

## Checklist

- [ ] `make check` passes locally.
- [ ] A bug fix carries a test that fails without it.
- [ ] A change to the CLI, a configuration variable, the HTTP API, or a file an
      audit writes is reflected in its spec, in `docs/`, and in `example.env`
      where it applies.
- [ ] A change to `method/` says so in the CHANGELOG, because it moves the
      method digest and later audits stop being comparable with earlier ones,
      and re-checks the cases in `evals/`.
- [ ] User-facing changes have an entry under `Unreleased` in `CHANGELOG.md`.

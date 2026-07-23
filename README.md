# replichai

Audits the reproducibility of research papers.

Give it a paper. It finds what the authors actually released, works out which parts of the work a machine can re-run and which parts need people, implements and runs the machine-runnable part, records every decision the paper left unwritten, and returns a verdict with a reproducibility rate you can decompose.

Built for human-computer interaction research, where a paper almost always contains two loops:

- an **inner loop** — the algorithm, model, system, or analysis pipeline, mechanically re-runnable;
- an **outer loop** — the study that validates it with human participants.

The inner loop is reproducible. The outer loop is not, and pretending otherwise is the most common way reproducibility claims go wrong. Placing that boundary precisely, and defending it, is the point.

## Install

```bash
uv venv && uv pip install -e ".[dev]"
```

## Use

```bash
export REPLICHAI_API_KEY=...            # never commit this
replichai audit https://arxiv.org/abs/2603.01795 --out runs
```

The audit writes a reproduction repository into `runs/<paper>/`:

| File | Contents |
|---|---|
| `README.md` | What the paper is, what the reproduction is, how to run it |
| `REPRODUCIBILITY.md` | Verdict and rate, the inner/outer boundary table, the hidden decisions, the open-science scorecard |
| `src/` | A best-effort implementation of the inner loop, runnable |
| `UNVERIFIED.md` | Everything that could not be confirmed, each with its specific blocker |
| `SOURCES.md` | The paper's identity and every artifact search performed, with its result |

Reproduction output is never committed to this repository. See [`docs/architecture.md`](docs/architecture.md).

## Layout

```
method/     the audit itself — instructions, rubric, task template (data, not code)
evals/      golden set: papers with known answers, used as a regression check
src/        orchestration and provider adapters
docs/       architecture and procedure
specs/      designs for work not yet built
tests/
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
pytest --cov=replichai --cov-report=term-missing
```

## License

Proprietary. All rights reserved.

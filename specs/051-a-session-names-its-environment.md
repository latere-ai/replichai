---
title: "A session names its environment"
status: built
---

# 051 — A session names its environment

**Status:** built.

**Scope:** letting a run choose what its machine runs, instead of every run getting one baseline image: the field in the session contract, what the `latere` and `anthropic-managed` runtimes offer, how a run names it, and what the run record keeps. The platform side, the images themselves and a fast start without a warm pool, is the Latere platform's design for environment profiles.

**Depends on:** [041a](041a-one-managed-session-contract.md) (the session contract), [041g](041g-latere-runs-a-session.md) (the Latere runtime), [046](046-one-run-is-one-draw.md) (the run record).

## Problem

Every run got one machine. On the Latere platform that was the `base` image, and the runtime refused any package the image did not hold. On Anthropic's managed agents each session created an environment of its own and deleted it at the end, so Anthropic's per-environment package cache never applied. The 49-run pilot of September 2026 showed what that cost: SciPy still missing minutes into a run, R packages built from source for 21 to 28 minutes, runs rewriting their analysis to avoid a dependency. The outer loop of [050](050-the-outer-loop-simulated.md) adds a machine with a display and a browser.

## Decisions

**The contract names an environment.** `SandboxSpec.Environment` names what the machine runs, such as `science`; empty is the runtime's default. A runtime lists the environments it offers in `Capabilities.Environments`, and `ValidateCapabilities` refuses a specification naming one it does not offer, before anything is created. A run never silently gets another environment.

**On `latere`, an environment is an image of the platform's catalog.** `base`, `science` (the Python statistics stack and R with the packages replications use) and `gui` (a display and a browser). The image goes into the agent's manifest, and since the agent's name is a digest of its manifest, each environment is its own agent with no change on the platform. Packages are validated against the environment's list. Which ones a session in the app may name is a platform setting an admin changes in Admin, Platform policy, read on every session, so a change needs no deploy: an image is usable only once the platform's catalog holds it, and an admin may hold one back that the platform has. `base` is always offered, and a new deployment offers `base` and `science`. A run from an operator's own machine may name any environment the runtime knows, since the operator names it explicitly. The list is a setting rather than an environment variable so the deployment's configuration does not grow with every environment, and it lives with the other platform policy. Reading which images exist from the Latere platform's catalog would remove the setting; the platform does not expose its catalog to API callers yet.

**On `anthropic-managed`, an environment is a package set on Anthropic's base image, and it is shared.** `science` installs the same Python packages and R from Ubuntu's own R packages. Sessions asking for the same environment, packages and network share one Anthropic environment, named from a digest of the three and found by name before one is created, so Anthropic installs the packages once. Releasing a session leaves a shared environment in place; a session started before sharing keeps deleting its own. An environment that installs packages behind a limited network now lets the package managers through, which Anthropic requires.

**A run names it.** `--environment` on `audit` and `run`, or `environment:` in a project file, for the file or one paper. The run record in `replication.json` carries `environment`.

## Outcome

Built as decided. The contract field and its check are in `internal/managed`; the Latere environments in `internal/runtimes/latere`; the shared Anthropic environments in `internal/managed/legacy` and `internal/provider/anthropic`, with the `science` package set in `internal/runtimes/builtin`. The admin's list is `latere_environments` in the platform settings, checked against the runtime's known environments in the console, which `internal/runtimes/builtin` names because only it may import the runtime. The server starts sessions on the default environment; choosing one per paper in the app, and choosing it from the paper automatically, are not built.

## Acceptance criteria

1. A specification naming an environment the runtime does not offer is refused before anything is created. (Tested on the contract, the Latere runtime, the shared-workspace runtime and the auditor.)
2. A Latere session on `science` runs the `science` image, and a package is judged against that image's list. (Tested.)
3. Two `anthropic-managed` sessions with the same environment, packages and network share one environment; another environment, another network or other packages get another; releasing a session never deletes a shared environment. (Tested.)
4. The environment a run named is in its run record. (Built for `audit` and `run`.)

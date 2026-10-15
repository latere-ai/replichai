# Documentation

Pages grouped by who reads them.

## Running audits

| Page | |
|---|---|
| [`../README.md`](../README.md) | what replichai is, the commands, and a first audit |
| [`cli.md`](cli.md) | every `replichai` command and its flags: runtimes, one audit, a project of papers, publishing, signing in to the app, and housekeeping |
| [`local-runs.md`](local-runs.md) | running audits on your own machine with the coding agent you already have: prerequisites, the sandbox, choosing a model, whole projects, filing results in the app, and troubleshooting |

## Reading a verdict

| Page | |
|---|---|
| [`procedure.md`](procedure.md) | what one audit does, step by step, and why each step is shaped the way it is: the artifact hunt, the inner/outer boundary, the hidden decisions, the verdict, grading, and what the system rather than the agent writes at the end |
| [`../method/`](../method/) | the method as the agent receives it: standing instructions, the rubric, and the task template |

## Operating a deployment

| Page | |
|---|---|
| [`self-hosting.md`](self-hosting.md) | running your own deployment from a clone: what you need, configuration, the identity provider, the first admin, compose for evaluation, the container image, and publishing |
| [`../example.env`](../example.env) | every environment variable the server and the CLI read, with its default and what it is for |
| [`../deploy/README.md`](../deploy/README.md) | how Latere deploys `replichai.latere.ai` on Kubernetes, as a worked example: the config secret, drafting, who may sign in, the rate-card check, outbound mail, and releasing |

## Changing the code or the method

| Page | |
|---|---|
| [`../CONTRIBUTING.md`](../CONTRIBUTING.md) | the code map, the quality gate, the test suites, the Postgres contracts, the frontend, adding a runtime, and changing the method |
| [`architecture.md`](architecture.md) | how the engine and the application are put together, the trust boundaries, and why each layer is split where it is |
| [`../specs/README.md`](../specs/README.md) | the design records: what was decided, what was built, and what is left |

## Studies of the audits

One audit is one run, and one run is one draw: in a September 2026 pilot, 49
runs of three papers differed from each other mostly because of the agent
system, the sandbox's network access, how the agent labeled its own results,
and the grader's revisions, more than because of the paper. A single run's
verdict is therefore one sample.

# ReplicHAI

**Replicates research with human participants, from the paper to the study.**
Give it a paper. It finds what the authors released, works out which parts of
the work a machine can re-run and which parts need people, implements and runs
the machine-runnable part, records every decision the paper left unwritten, and
returns a verdict: a per-component table saying what was reproduced, what only
partly, and what was blocked, each with its evidence or its specific blocker.

That is the first half. The goal is the study itself: where the analysis
reproduces reliably across repeated runs, an agent builds the paper's
experiment, simulated participants drawn from the population the paper sampled
take part in it, and the paper's conclusion is checked against what they did.
The second half is coming soon.

[![CI](https://github.com/latere-ai/replichai/actions/workflows/ci.yml/badge.svg)](https://github.com/latere-ai/replichai/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/latere-ai/replichai)](https://github.com/latere-ai/replichai/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/latere-ai/replichai)](go.mod)
[![License: AGPL-3.0-or-later](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue.svg)](LICENSE)

**Status.** Pre-1.0. The engine, the command line and the web application run
real audits, and Latere AI operates the web application at
[replichai.latere.ai](https://replichai.latere.ai). The files an audit writes
and the HTTP API can still change between minor versions;
[`CHANGELOG.md`](CHANGELOG.md) says when they do.

## Who it is for

Research with human participants: human-computer interaction, psychology,
social science, and any other field where a finding rests on what people did.
A paper of that kind almost always contains two loops:

- an **inner loop**: the algorithm, model, system, or analysis pipeline, which
  a machine can re-run;
- an **outer loop**: the study that validates it with human participants.

ReplicHAI works through both, in order:

1. **What replicates.** An audit re-runs the inner loop from what the authors
   released and says, claim by claim, what reproduced. The audit never
   simulates participants: it places the boundary between the two loops and
   reports the inner one.
2. **Whether to go on.** Repeated runs of one paper show whether its inner
   loop reproduces reliably, and whether its study could be served by a
   machine. Only a paper that passes continues. *Repeated runs are built; the
   gate is coming soon.*
3. **The experiment.** An agent builds the study the paper ran, for example
   the website that shows participants its stimuli, and checks it against the
   paper's protocol. *Coming soon.*
4. **The study, simulated.** Simulated participants with a declared
   population profile take part in that experiment through its interface, the
   paper's own analysis runs over their responses, and the result is compared
   with the paper's conclusion. A simulated result is always labeled as one,
   and reported with how well simulation matched people on studies whose
   human replications are known. *Coming soon.*

## Try it

**The web application, with nothing but Docker or Podman.** This starts
Postgres and the server on the free `memory` runtime, a stand-in that runs no
model, so you can click through projects, sessions and verdicts at no cost:

```bash
git clone https://github.com/latere-ai/replichai && cd replichai
make up          # http://localhost:8080; sign-in is bypassed on loopback
```

**A real audit on your own machine**, with a coding agent you are already
logged in to (Claude Code or Codex). It runs confined to one folder by the
operating system's sandbox, and the vendor bills your own plan:

```bash
go build ./cmd/replichai                       # Go 1.27 or newer
npm install -g @anthropic-ai/sandbox-runtime   # the sandbox
brew install ripgrep                           # Linux also needs bubblewrap and socat
./replichai audit https://arxiv.org/abs/2603.01795 --runtime local
```

A paper reference is a DOI, an arXiv URL or bare ID, a publisher URL, or a PDF
URL. [`docs/local-runs.md`](docs/local-runs.md) covers the prerequisites per
platform, choosing the coding agent and the model, running inside a container,
and what the agent can and cannot reach.

## What an audit writes

An audit writes a reproduction repository into `runs/<paper>/`:

| File | Contents |
|---|---|
| `README.md` | What the paper is, what the reproduction is, how to run it |
| `REPRODUCIBILITY.md` | The verdict and the per-component reproduction table first, then the inner/outer boundary, the hidden decisions, and the open-science scorecard |
| `src/` | A best-effort implementation of the inner loop, runnable |
| `UNVERIFIED.md` | Everything that could not be confirmed, each with its specific blocker |
| `SOURCES.md` | The paper's identity and every artifact search performed, with its result |
| `verdict.json` | The same findings as data: boundary rows, hidden decisions, scorecard, what was released |
| `instrument.json` | The declared study protocol, the analysis entrypoint, and whether the outer loop could be hosted |

If `verdict.json` and `REPRODUCIBILITY.md` disagree, the JSON is authoritative;
the web application renders a session's findings from it. Any overall
reproduction rate is a derived footnote to the table, never a figure to compare
across papers, because how many components a paper splits into is itself a
judgment. [`docs/procedure.md`](docs/procedure.md) explains why.

## Ways to run it

| | What you need | Who pays for the model |
|---|---|---|
| The web application at [replichai.latere.ai](https://replichai.latere.ai) | an account there | you buy credits in the app |
| The command line, `--runtime local` | a Claude Code or Codex login, or an OpenAI-compatible endpoint | your own vendor plan |
| The command line, `--runtime anthropic-managed` (the default for `audit`) | an Anthropic API key with access to Managed Agents | Anthropic bills the key |
| The command line, `--runtime latere` | a Latere account and a personal access token in `LATERE_API_KEY` | your own Latere wallet |
| Your own deployment of the server | Postgres, an OpenID Connect issuer, a model credential | you |

`replichai run` takes a project file and audits many papers at once, in
parallel and resumably, and can run one paper several times. With `--publish`,
each reproduction gets its own GitHub repository and the agent's commits become
its history; name the GitHub organization it goes under with `--owner`.
[`docs/cli.md`](docs/cli.md) is the full command reference and
[`docs/self-hosting.md`](docs/self-hosting.md) covers running your own server.

## The method

What an audit does is not in the code. [`method/`](method/) holds the agent's
standing instructions, the rubric every run is graded against, and the task
template, and their digest is recorded with every published reproduction. A
grader inside the loop checks each run against the rubric, and a run that falls
short revises and is graded again, up to a budget.

One audit is one run, and one run is one sample. A second run of the same paper
can split it into different components and reach different outcomes for them,
and the coding agent, the model and the network a run had move its result too.
Every run records what it ran under, and a project file can run one paper
several times to see how far its runs agree.

## Not built yet

- The outer loop: building a paper's experiment and running it with simulated
  participants, and the gate that decides which papers go on to it. The engine
  for binding a study's instrument to a new data source exists; nothing in the
  application reaches it yet.
- Submitting a paper as an uploaded PDF. A paper is referenced by an identifier
  or a URL.
- A published container image for `--sandbox container`. You build one and name
  it with `--image`.

The design records in [`specs/`](specs/README.md) say what is planned and why.

## Documentation

| Page | For |
|---|---|
| [`docs/local-runs.md`](docs/local-runs.md) | running audits on your own machine, alone or a whole project at once |
| [`docs/cli.md`](docs/cli.md) | every command and flag |
| [`docs/procedure.md`](docs/procedure.md) | what one audit does and why, for anyone reading or relying on a verdict |
| [`docs/self-hosting.md`](docs/self-hosting.md) | running your own deployment of the web application |
| [`docs/architecture.md`](docs/architecture.md) | how the engine and the application are put together |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | building, testing, and changing the code or the method |
| [`specs/`](specs/README.md) | the design records: what was decided, what was built, and what is left |

[`docs/README.md`](docs/README.md) is the full index.

## Contributing, security, conduct

Contributions are welcome; [`CONTRIBUTING.md`](CONTRIBUTING.md) explains how to
set up, test and send a change. Report a vulnerability privately as
[`SECURITY.md`](SECURITY.md) describes, never in an issue. Participation is
governed by the [code of conduct](CODE_OF_CONDUCT.md).

## Citing

If you cite a reproduction ReplicHAI produced, cite the software too, with the
version and method digest recorded in that reproduction's `replication.json`.
[`CITATION.cff`](CITATION.cff) has the details, and GitHub's "Cite this
repository" button renders it.

## License

[GNU Affero General Public License v3.0 or later](LICENSE). If you run a
modified version as a network service, the license requires you to offer its
users the source of your version.

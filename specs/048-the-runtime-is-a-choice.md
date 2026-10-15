---
title: "The runtime is a choice"
status: designed
---

# 048 — The runtime is a choice

**Status:** designed. Who pays for a Latere session is decided (the person's own Latere wallet); the app's half waits on two Auth changes described under Price and charge.

**Scope:** the person who starts a session picks where it runs and on what model, in the app and on the command line. Which runtimes a deployment offers, the model list each runtime answers with, how a session records the runtime and model it ran on, and how a session run on the Latere platform is priced and charged. The Latere runtime itself is [041g](041g-latere-runs-a-session.md); this spec is the choice in front of it.

**Depends on:** [041](041-the-runtime-is-not-the-model-vendor.md), [041c](041c-a-session-remembers-its-runtime.md), [041d](041d-models-and-meters-name-their-source.md), [041g](041g-latere-runs-a-session.md), [031](031-the-model-is-a-choice.md), [011](011-what-a-session-costs.md), [045k](045k-a-local-session-reaches-the-cloud.md), [046](046-one-run-is-one-draw.md).

**Changes:** three earlier decisions, each for the reason given where it is replaced. [041](041-the-runtime-is-not-the-model-vendor.md) put "exposing runtime choice to researchers" out of scope and [041c](041c-a-session-remembers-its-runtime.md) made the runtime an operator policy; both are replaced by Runtimes a deployment offers. [031](031-the-model-is-a-choice.md) refused a concrete model id as user input; on the Latere runtime that refusal is replaced by A specific model, and on Anthropic it stands.

## Problem

Every hosted session runs on Anthropic Managed Agents, on one of three Claude models. Nobody chose that: the deployment sets `REPLICHAI_RUNTIME` once, and the person starting a session can only pick between three tiers of the same vendor.

For a research product that is a measurement problem, not only a commercial one. The repeat-run pilot found that the agent system and the model moved a run's result more than the paper did, and 046 decided that a study records the environment of every run and varies it on purpose. A study that can only vary the model inside one vendor cannot separate "this paper is underspecified" from "this vendor's models read it this way".

The Latere platform already runs a whole agent stack: Topos runs the agent and its session, Cella the sandbox, Lux any model the platform serves. 041g adds it as a second hosted runtime. What is missing is the choice: a person cannot ask for it, a session does not record that it was asked for, and nothing says who pays.

## Decisions

### Runtimes a deployment offers

A runtime is offered when the deployment has configured it, and only then:

| Runtime | Name on record | Offered when |
|---|---|---|
| Anthropic | `anthropic-managed` | `ANTHROPIC_API_KEY` is set |
| Latere | `latere` | the Latere platform is configured (041g, Configuration) |
| OpenAI | `openai` | designed, not built: never offered yet |
| Local | `local` | on the command line only; the server never offers it |
| Memory | `memory` | in development only |

The server builds every offered runtime at boot, and one that fails to build fails boot, as the selected runtime does today. A deployment that names a runtime in `REPLICHAI_RUNTIME` keeps it as the default for a session that names none. `GET /models` (031) grows into the one answer the app reads: each offered runtime, its label, whether it is the default, its Effort levels with their prices, and, for Latere, the models a person may pick.

The name on record is `latere`, not 041c's provisional `latere-topos`. Nothing ever persisted `latere-topos`, so the change costs nothing, and the runtime is the whole platform (Topos, Cella and Lux), not one of its parts.

A session start naming a runtime the deployment does not offer is `400 bad_runtime`, before `Enqueue`, so no job row and no hold exist for it.

### Effort is the word, everywhere

The three tiers are Effort, as everywhere else in the Latere family: **Quick**, **Thorough** and **Sustained**. "Model tier", "best", "balanced" and "fast" leave the app's copy. The stored values do not change: a job's `tier` column keeps `fast`, `balanced` and `best`, which read as Quick, Thorough and Sustained, so no row is rewritten and an old record still reads the way it was written. The command line accepts both spellings.

What a level runs depends on the runtime:

- **Anthropic** keeps 031's mapping through its own catalog: Quick is the Haiku model, Thorough the Sonnet model, Sustained the Opus model.
- **Latere** runs the platform's own level of the same name. Which models serve a level is the platform administrator's choice, set once for every Latere product, and may change between sessions. ReplicHAI does not keep a second copy of that list.

### A specific model, on Latere only

Below the three levels, the Latere runtime offers "A specific model": any model the platform serves to the person starting the session, by its platform name (for example `anthropic/claude-sonnet-5` or `qwen/qwen3-coder-480b`). The list comes from the platform; ReplicHAI does not maintain one. The server checks the chosen name against that list at session start and refuses an unknown one with `400 bad_model`.

On Anthropic the exact model stays a command-line option, as 031 decided. The reason 031 gave (a typo reaching the vendor and being charged at the dearest rate on the card) does not hold on Latere, where the platform refuses a model it does not serve and prices the one it does.

### What a session records

At enqueue the job records three things beside the tier, in the transaction that inserts it (the third, `payer`, is under Price and charge):

- `runtime`: the canonical runtime name the person chose, or the deployment default when they chose none.
- `model`: an exact model when the person picked one, empty when they picked an Effort level.

The worker binds its router to the job's runtime (041c's route snapshot), so a session runs where it was asked to run even after the deployment's default moves. `Status.Model` records the model the session resolved to, as today.

A session on a Latere Effort level can be served by more than one model: the platform may move a turn to another model of the level when one is busy. The contract's rule that a session's resolved model never changes stays true, because `Status.Model` is the model the platform resolved the session to when it was created. Every model that served a turn is on that turn's events (`Event.Usage.Model`), and the run record (046) gains `models`, the list of every model that served, so a study can see a mixed run rather than read it as one model's.

### Price and charge

**The person's own Latere wallet pays.** The owner decided this on 2026-10-16, for sessions started from the app as well as from the command line. ReplicHAI starts a Latere session as the person who pressed Start, and the platform charges that person's wallet, exactly as for a session they start in the console or the chat. ReplicHAI takes no hold, posts no charge, and never refuses a Latere start for an empty ReplicHAI wallet; the platform refuses it, with its own reason, when the person's Latere wallet cannot pay. A session run on the person's own machine already works this way (045k: it never touches the credit ledger).

The rejected answer was ReplicHAI credits: ReplicHAI running the session under a platform organization of its own and charging the project's pot. It would have kept one balance for a ReplicHAI user, at the cost of ReplicHAI holding a second wallet on the platform, which the platform's own rule (a product may not hold a wallet of its own) argues against.

**Who pays is recorded on the job, at enqueue.** A runtime's catalog entry says who pays for its sessions: the project (Anthropic, memory) or the person (Latere). `Enqueue` writes that as the job's `payer`, `project` or `person`, in the inserting transaction, next to `runtime` and `model`. It is written rather than derived later from the runtime's name, so a change to a runtime's entry never re-bills a session that already ran. The two paths then differ in three places and nowhere else:

- `Enqueue` takes the start reserve (`takeReserveTx`) and snapshots the rate card only when the payer is the project. A person-paid job takes no hold, so an empty pot does not refuse it.
- The terminal write settles (`settleTx`) only when the payer is the project. A person-paid job has no hold to release and no charge to post, so the ledger never sees it, as it never sees an imported local session (045k).
- A job's cost is priced from the rate card for a project-paid job, and is the cost the platform reported for a person-paid one. The card is never asked to price a Latere session.

Existing rows have no `payer` and read as `project`, which is what every one of them was.

What a session costs is what the platform reports: the sum of `cost_usd_micro` over the session's model calls, which Topos totals as the session's `spent_cost_usd_micro`. ReplicHAI's rate card does not price a Latere session, because a Latere session can run any of hundreds of models and the platform is the one that prices them. The cost is shown on the session as today, marked as charged to the person's Latere wallet rather than to the project.

**Acting for the person in the background.** A session runs for hours after the browser request that started it has ended, and ReplicHAI's server reads its events, starts its grading rounds and collects its files throughout. The server therefore keeps the person's own platform sign-in: the app's sign-in asks for a refresh token (`offline_access`), the server stores it encrypted, and mints a short-lived token for the platform's API (`api.latere.ai`) from it when a job needs one. Two changes on the platform's side come first, and neither is ReplicHAI's to make: the platform's identity rules allow a product to keep a person's sign-in for work that person started, which they did not before (the owner decided it on 2026-10-16), and Latere Auth, the platform's identity provider, lets ReplicHAI's web client ask for `offline_access` and both ReplicHAI clients mint tokens for `api.latere.ai`. Until both are live, a deployment does not offer the Latere runtime in the app, and the command line reaches it with the person's personal access token.

A refresh token rotates on every use, and presenting a spent one ends the whole sign-in. The server therefore refreshes one person's token under a lock, stores the new token before it uses the access token, and keeps each access token until it expires rather than refreshing per call.

### On the command line

`--runtime` already selects a runtime and `--model` already takes a tier or `provider/id`. They gain:

- `--runtime latere`, configured as 041g describes. It acts as the person signed in with `replichai login` once Auth lets that sign-in mint for the platform's API; until then it takes the person's personal access token from `LATERE_API_KEY`.
- `--model quick|thorough|sustained` beside `fast|balanced|best`.
- `replichai models [--runtime <name>]`, which lists what `--model` accepts on that runtime: the three levels and, on Latere, every model the person may use.

### The OpenAI slot

OpenAI is designed and not built. When it is added it is one more row in Runtimes a deployment offers, its Effort levels map to its own models as Anthropic's do, and nothing above the runtime changes. Which OpenAI product it sits on (Codex cloud tasks, or background Responses with a hosted container, as [041f](041f-openai-responses-runs-a-session.md) designed) is decided when that work starts.

## Acceptance criteria

1. With Anthropic and Latere configured, `GET /models` lists both runtimes with their Effort levels and prices, marks the default, and lists Latere's models; with only Anthropic configured it lists one runtime and the app shows no runtime choice.
2. `POST /projects/{id}/sessions` with `runtime: "latere"` enqueues a job whose `runtime` is `latere`, and the worker runs it on the Latere runtime even when the deployment's default is Anthropic. A restart between enqueue and start does not change where it runs.
3. An unknown or unconfigured runtime is `400 bad_runtime`, and an unknown Latere model `400 bad_model`; afterwards the project has no new job and no new hold.
4. A session that names no runtime runs on the deployment default, with the same model and the same charge as before this spec.
5. The app's control says Effort, Quick, Thorough and Sustained; a browser test drives it, picks Latere and a specific model, and asserts the request carries both.
6. `replichai audit <paper> --runtime latere --model thorough` runs a real paper end to end on the live platform, and `--model <provider/id>` runs on that model.
7. A Latere session whose turns were served by two models records both in its run record, and the model it started on in `Status.Model`.
8. A Latere session posts no hold and no charge to the project's ledger, a project with an empty pot can still start one, and the session shows the cost the platform reported for it.
9. The server refreshes one person's platform sign-in at most once at a time across its workers, and a test that races two jobs of one person never presents a spent refresh token.

## Out of scope

- A project-level default runtime or level. A choice nobody was asked to make permanent stays per session, as 031 decided for the tier.
- Mixing runtimes inside one session. A session runs where it started (041c).
- Choosing the reasoning depth of a model. On Latere the platform sets one reasoning level per Effort level, and a specific model runs at its own default.

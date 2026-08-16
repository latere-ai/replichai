# 039 — A description can be drafted, by whichever model the deployment configures

**Status:** built. All nine criteria hold.

The seam is `internal/draft`: a `Model` interface, an Anthropic Messages client and an OpenAI-compatible one, both speaking their protocol over plain HTTP rather than through a vendor SDK — this is one POST with four fields, and the SDK's session machinery is what the package exists to avoid. The composition root resolves the configured model *per call* rather than at boot, so an admin changing it does not need a deploy.

Two details the design did not anticipate. The two protocols report cached prompt tokens differently — Anthropic's `cache_read_input_tokens` is *beside* the input count and OpenAI's `cached_tokens` is *inside* it — so the OpenAI adapter subtracts them out before pricing; counted as they arrive, a cached prompt would be billed at the input rate and the cache rate at once. And a non-Anthropic credential is read from one fixed environment variable rather than one an admin names: letting the settings row name the variable would have let an admin point the base URL at their own server and the credential at *another* of the deployment's secrets, which is an exfiltration primitive rather than a configuration option.

Pricing needed no new field: the rate card already prices by model id. What it needed was the *refusal* — an exact card entry, never the card's fail-expensive fallback, which exists so an unrecognised session model is charged the dearest rates rather than nothing and would have billed Opus rates for a local model.

**Scope:** the owner of a project can ask for a description to be drafted from what the project already holds. The draft is one short model call, on a model the deployment's admin configures — which need not be Anthropic's — and it is charged to the project. This is the first single-turn model call in the codebase, and it brings the seam for every cheap task after it. Backend and frontend. One migration (the ledger's new kind needs no schema; the platform setting does).

**Depends on:** [038](038-a-project-says-what-it-is-about.md) (the field being drafted), [037](037-a-paper-carries-its-bibliography.md) (what the draft is written from), [011](011-what-a-session-costs.md) (the ledger it charges), [006k](006k-the-platform-admin-console.md) (where an admin configures it), [031](031-the-model-is-a-choice.md) (the tier vocabulary this deliberately does *not* reuse).

## Problem

Writing a sentence about a project you already assembled is a chore, and the material for it is sitting in the project: a title, and now ([037](037-a-paper-carries-its-bibliography.md)) each paper's authors, venue and year. A model does this well and it costs a fraction of a cent.

Three things are missing, and each is a design question rather than a line of code.

**There is no way to make a small model call.** Every model call in the codebase is a managed-agents run: a workspace, a worker, a start, a poll loop, artifacts, a close (`internal/audit`, `internal/experiment`). `provider.Provider` has no method that takes a prompt and returns a string. Drafting a paragraph through that machinery would pay tens of seconds and a session's worth of bookkeeping for two hundred tokens.

**There is no way to charge for one.** Money moves through holds and settlements keyed on a job id — `Hold(ctx, holder, jobID, amount)`, `Settle` made exactly-once by a unique index on `job_id`. A draft has no job. The only existing precedent for a debit that is not a session is `RefundPaid`'s wallet-level `adjust`, idempotent on an `external_ref`.

**The model is assumed to be Anthropic's.** `ResolveModel` maps `best/balanced/fast` onto `opus/sonnet/haiku` by substring-matching Anthropic's live catalogue, and the rate card is keyed by Anthropic model id. A deployment that wants its cheap tasks run by something else — a local model, an OpenAI-compatible endpoint, whatever is cheapest that month — has nowhere to say so.

## Decisions

**A drafting model is a seam of its own, not a tier.** A new interface takes a prompt and returns text plus token counts. It is deliberately not `provider.Provider`: that interface is about *sessions*, and every method on it assumes a durable run somebody can rejoin. The tiers of [031](031-the-model-is-a-choice.md) stay what they are — a choice a person makes about the agent that reproduces their paper — and the drafting model stays an operator's configuration, because nobody starting a project should have to have an opinion about which model writes a summary.

**Two implementations, one of them not Anthropic's.** An Anthropic Messages client, and an OpenAI-compatible HTTP client — which is the protocol a local llama.cpp, vLLM, Ollama, OpenAI, Groq, Together and OpenRouter all speak. Configuring "some other provider" is then a base URL and a model id rather than a code change, which is what makes this an interface rather than a second hard-coded vendor.

**Choosing the vendor already in use costs nothing.** A deployment holds an Anthropic credential because that is what runs its sessions; asking an operator to configure a second one for the same vendor is paperwork, not security. So picking Anthropic is a model id and nothing else — the endpoint is the vendor's own and the key is the one already there. A *different* vendor is the case that needs an endpoint and a key, because that is a credential the deployment does not have.

**The credential never goes in the database.** The admin's setting names *which* credential the deployment holds (`DRAFT_API_KEY`, and the base URL and model id may come from settings), and the key itself stays in the environment with `REPLICHAI_API_KEY` and the forge token. A settings table is read by every admin, dumped into every backup and printed by every debug endpoint that ever gets written; an API key in it is a key that leaks eventually. The admin console shows whether a credential is present, never what it is.

**An unpriced model does not draft.** The charge is computed from tokens exactly as a session's is, so a configured model carries its own price per million tokens in the same setting. With no price there is no charge, and a call nobody can price is refused rather than run for free — the rule [011](011-what-a-session-costs.md) already applies to sessions, for the same reason.

**A draft is charged to the project, as a charge with no job.** A new ledger kind, idempotent on a key of its own, so a retried request bills once and the project's statement shows what its drafts cost beside what its sessions cost. The wallet's arithmetic stays complete: every token this platform spends on a project is accounted to that project.

**The draft is a suggestion.** It lands in the description editor for the owner to read, change and save. Nothing is written to the project until a person saves it, which is what keeps [038](038-a-project-says-what-it-is-about.md)'s field a human one. A draft the owner discards still cost money and is still charged — the tokens were spent.

**The prompt is what the project already holds.** Its title, and its papers' bibliographies. No PDFs, no transcripts, no verdicts: a description says what the project is about, and what it is about is which papers are in it. This also keeps the call small enough for the price to stay in the fractions of a cent it is being sold as.

**With no model configured, there is no button.** A deployment that has not configured drafting shows nothing to click, rather than a control that fails. The rest of [038](038-a-project-says-what-it-is-about.md) works untouched — descriptions are typed.

## Acceptance criteria

1. A drafting call goes through one interface with two implementations, and the OpenAI-compatible one is exercised against a fake server speaking that protocol.
2. An admin configures provider, base URL, model id and price per million tokens; the credential is read from the environment and never stored or returned by any endpoint.
3. Requesting a draft for a project returns prose derived from the project's title and its papers' records, and writes nothing to the project.
4. The call is charged to the project at the configured price, appears in its statement as a draft, and a retried request with the same key charges once.
5. A project with no available credit is refused before the call is made.
6. A deployment with a configured model but no price refuses to draft, and says so to the admin rather than to the owner.
7. With no model configured, the project screen offers no draft control and the description is still editable.
8. A draft the owner discards leaves the description as it was, and leaves the charge in place.
9. A non-member cannot draft for a project they may read.

## Out of scope

Drafting anything else — a paper's summary, a session's write-up. The seam is general and the second caller can come later; one caller is enough to prove it.

Streaming the draft. It is a paragraph; it arrives when it arrives.

Choosing a drafting model per project. The model is an operator's decision about cost, not a project's about method — and [031](031-the-model-is-a-choice.md) is where a person's model choice already lives, for the run that actually matters.

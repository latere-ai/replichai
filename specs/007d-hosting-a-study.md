# 007d — Hosting a study for participants

**Status:** draft
**Scope:** the `human` data source — serving an extracted protocol as a public participation page, collecting trial data for a project, and handing it to the project's researchers. The platform's first unauthenticated write path, and the only spec in the track that touches real people. A child of [007](007-from-reproduction-to-replication.md).
**Depends on:** [007b](007b-experiments-over-instruments.md) (the experiment model; a collected dataset is a `human`-source experiment), [007a](007a-the-study-protocol-as-an-artifact.md) (the protocol and the servability gate), [006a](006a-http-api-and-streaming.md), [006b](006b-job-model-and-persistence.md), [006c](006c-identity-and-access.md), [006e](006e-frontend.md).

## Problem

[`docs/procedure.md`](../docs/procedure.md) §7 says a paper's claim about the world rests on the outer loop and the sample that was collected. Everything else in this track works with data that already exists. This spec is the one that collects a new sample, which is the only thing that can actually test such a claim — and it is last on purpose, because it is where the platform stops handling files and starts handling people.

It also inverts the application's security posture. Every route [006](006-web-application.md) designs sits behind a sign-in and a grant; a participation page is by definition open to strangers, and those strangers *write*. That is new attack surface and a new class of stored data, and most of this spec is about those two facts rather than about the study.

## Decisions

**The page comes from the instrument; the platform is a generic runner.** The `study/` bundle is a static, self-contained web application the agent writes from `protocol`. The platform serves it and accepts responses against `protocol.measures`, so it holds no per-paper code and stays paper-agnostic — the same posture that keeps `method/` as data rather than logic.

**The bundle is agent-written code served to the public, so it never shares an origin with a session.** It is served from a distinct origin that sets no session cookie and is never sent one, under a strict CSP with no external fetch. The platform's own authenticated pages embed it only in a sandboxed frame. An agent-authored script and a signed-in researcher's session must not be reachable from each other, and this is asserted against the served headers rather than left to convention ([007](007-from-reproduction-to-replication.md), criterion 6).

**The anonymous write path is bounded on every axis.** A public `POST` into Postgres gets no benefit of the doubt:

| Bound | Shape |
|---|---|
| Capability | A run-scoped opaque token in the URL; no token, no route. Tokens are per *run*, revocable by closing it. |
| Schema | Responses are validated against `protocol.measures` — declared columns, types, and ranges. Anything unshaped is rejected, not stored for later cleaning. |
| Idempotency | One write per `(participant, trial)`. A replay overwrites nothing and is not a second observation. |
| Volume | A per-run cap on participants and on total responses; reaching it closes the run rather than growing the table. |
| Rate | Per-source throttling, on a salted short-lived hash — never a stored address. |
| Time | A run has an expiry and a lifecycle: `draft → open → closed`. Only `open` accepts writes. |

**Collected data is de-identified by construction, which is why free text is refused.** The response schema admits only the protocol's declared closed-form measures — reaction time, accuracy, choice, Likert — plus an opaque per-session participant token. There is no field in which an identifier could be stored, so de-identification is a property of the schema rather than a cleaning step someone has to remember.

Free-text measures would break that property, because a participant can type anything into one, including their name. Hosted runs therefore refuse a protocol with a free-text measure. This costs little: qualitative coding is already outside [007](007-from-reproduction-to-replication.md)'s scope, so there would be nothing to do with the text.

**Participant data never reaches a repository, a mount, or the registry — and reaches a sandbox only as the de-identified dataset it is.** This is the corrected form of [007](007-from-reproduction-to-replication.md)'s rule. Analysing collected data means running the instrument's pipeline over it, and [007b](007b-experiments-over-instruments.md) runs pipelines in the provider sandbox; a rule forbidding the sandbox outright would make a `human`-source experiment impossible, which cannot be the intent.

So the rule is: collected data is attached as a session resource *outside the mount*, exactly as an uploaded dataset is, and it is safe there for a reason specific to this spec — the schema above guarantees it carries no identifier. It never enters the mounted repository, never enters the manifest, and never reaches [003](003-reproduction-registry.md)'s forge-derived registry. `publish.Classify` gains its participant origin as the belt-and-braces refusal 007 describes, for a file that should never be presented to it.

**Consent precedes the first trial, and withdrawal deletes.** Consent text is the researcher's, versioned, and shown before any measure is recorded; each session records which version it accepted and when. A participant can withdraw from the page, which deletes that session's responses — not a flag, an absence, on the same terms [007b](007b-experiments-over-instruments.md) sets for deleting an uploaded dataset. Retention is declared per run against a platform maximum, and expiry deletes.

**Opening a run requires an ethics attestation, and the platform never evaluates it.** Moving `draft → open` requires an explicit attestation naming the approving body and its reference, recorded with the attesting principal and timestamp. The platform does not review, verify, or infer approval — it records who asserted what, which is the only honest thing a piece of software can do here, and it is the same accountability shape as [006](006-web-application.md)'s recorded `made_public_by`.

**A run also requires a servable protocol, refused server-side.** [007a](007a-the-study-protocol-as-an-artifact.md)'s closed criteria list is the gate, and a refusal cites the criteria that failed. Both this and the attestation are checked in the server regardless of what the UI offers.

**A human cohort is the third `Cohort`, so analysis is not rebuilt.** Collection produces a dataset; the dataset becomes a `human`-source experiment; the experiment runs the instrument's declared pipeline through [007b](007b-experiments-over-instruments.md)'s runner. Nothing about analysing a new sample is special, which is the payoff of having generalised the data source rather than adding features.

The result is a **re-observation** — the first thing the platform produces that can bear on a paper's claim about the world. And it still is not a verdict: the platform reports the statistics beside `protocol.reported`, and whether a new sample upholds the paper is the researchers' conclusion to draw and defend.

**Recruitment and payment stay outside.** The platform issues a participation link and a completion code a panel can verify. It does not recruit, screen, pay, or hold a participant's identity — the researcher brings their own panel, and the completion code is the entire integration surface.

## Interface (sketch)

```
the participation origin — no session cookie is set or accepted here
GET  /s/{run}                     -> the instrument's study/ bundle, strict CSP
POST /s/{run}/sessions            -> { participant_token, consent_version }
POST /s/{run}/responses           -> declared measures only; idempotent per (participant, trial)
POST /s/{run}/complete            -> { completion_code }
POST /s/{run}/withdraw            -> deletes this session's responses

the operator API — behind 006c's gate, project members only
POST   /projects/{id}/runs                    { instrument, protocol_run_config }
POST   /projects/{id}/runs/{run}/open         { ethics: { body, reference } }
POST   /projects/{id}/runs/{run}/close
GET    /projects/{id}/runs/{run}              -> lifecycle, counts, caps, retention
GET    /projects/{id}/runs/{run}/dataset      -> export, de-identified by construction
DELETE /projects/{id}/runs/{run}/dataset
```

```go
// internal/experiment: the third Cohort. Collect does not drive anything — it
// reads what participants already wrote through the public path.
type HumanCohort struct {
	Run   RunID
	Store ResponseStore
}
func (h HumanCohort) Kind() string { return "human" }
```

## Acceptance criteria

1. The participation origin sets no session cookie, is never sent one, and serves the bundle under a CSP permitting no external fetch. Asserted on the response headers; the platform's embed is sandboxed.
2. Every bound holds, each asserted directly: an absent or revoked token is refused; an unshaped response is rejected rather than stored; a replayed `(participant, trial)` writes once; reaching the volume cap closes the run; a `draft` or `closed` run accepts nothing.
3. No stored source address — rate limiting uses a salted hash with a short lifetime, asserted by reading the store.
4. A protocol containing a free-text measure is refused for hosting, with the measure named.
5. Consent is recorded with its version before the first measure exists for that session; a session with no recorded consent has no responses.
6. Withdrawal deletes that session's responses — absence, not a flag — and the same holds for dataset deletion and for retention expiry.
7. Opening a run without an ethics attestation is refused server-side; the attestation records body, reference, principal, and time.
8. A non-servable protocol is refused for hosting with the failed criteria cited, even when the request is forged past the UI.
9. A `human`-source experiment runs through 007b's existing runner with no analysis path of its own, and collected data reaches the sandbox only outside the mount.
10. Collected data never appears in a repository, a manifest, or the registry — asserted structurally, and again by `Classify` refusing a participant-origin path.
11. Only project members reach the operator routes for a run, and project membership admits nobody to the platform ([007](007-from-reproduction-to-replication.md), criterion 7).
12. The reported outcome sits beside `protocol.reported` and emits no pass/fail judgement about the paper.
13. No network and no credential: the public path is tested against the server directly, the analysis against the in-process provider, identity against 006c's stub issuer.

## Out of scope

Recruitment, screening, panel integration, and payment — a link and a completion code are the whole surface. Reviewing or verifying ethics approval. Free-text and qualitative measures, and the coding they would need. Longitudinal, multi-session, moderated, and apparatus-based studies, which the servability criteria already exclude. Participant identity of any kind, including accounts, emails, and cross-run linkage. Deception protocols and the debrief machinery they require. Deciding whether a replication succeeded. Meta-analysis across runs or projects.

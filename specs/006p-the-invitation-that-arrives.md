---
title: "The invitation that arrives"
status: built
---

# 006p — The invitation that arrives

**Status:** built. `latere.ai/x/pkg/email` (v0.39.0) owns transport only — `New` selects Mailgun → SMTP → log, `Sender.Send(ctx, to, subject, htmlBody)` delivers, `rejectCRLF` refuses a forged header, and the two SMTP paths now share one envelope exchange; 98.8% covered, including an in-process SMTP server for both TLS modes and a fuzz test on the header check. auth keeps its two messages and their escaping over that transport, its test-mode code-capturing log sender stays local, and its full suite passes unchanged (its `internal/email` at 100%). replichai's `internal/notify` owns what the platform says to somebody who is not looking at it; `POST /projects/{id}/members` sends the invitation best-effort behind a `Notifier` seam, and `buildNotifier` wires the transport from the environment, failing at startup on a key with no domain. Covered by `internal/notify` at 100%, seven handler tests (sent, named from the session, survives a failed send, not repeated for an existing member, sent again after a removal, silent when the add is refused, works with no notifier wired), and four startup-wiring tests. Config lands in `example.env`, `docker-compose.yml`, `deploy/base/deployment.yaml`, and `deploy/README.md` together.
**Scope:** a project invitation reaches the person it names. A shared `latere.ai/x/pkg/email` owns transactional mail transport for every latere service; replichai grows the invitation mail and the configuration to send it. A child of [006f](006f-taking-a-reproduction-out.md).
**Depends on:** [006f](006f-taking-a-reproduction-out.md) (the members API the mail announces), [006c](006c-identity-and-access.md) (the verified email a membership is seeded against), [006q](006q-sign-in-is-open-budget-is-not.md) (what the mail can honestly promise about signing in).

## Problem

Inviting a collaborator wrote a database row and nothing else. `POST /projects/{id}/members` seeds the owner as lead, adds the email, returns `204` — and the person named has no way to learn any of it happened. The UI reports "pending", which is true and useless: the invitee is not deciding, they were never told. The only path in was for the owner to send the project URL out-of-band, through a channel the platform has no part in.

This is not a missing notification. It is a hole in the model: membership is the one place where the platform asserts something *about a person who is not present*, and it had no way to reach them.

The auth service has solved the transport half already — Mailgun with an SMTP fallback and a log-only sender for local runs, `rejectCRLF` on every header, an OTel-instrumented client. replichai has none of it, and copying it would give two services two divergent copies of the same careful code.

## Decisions

**The shared package owns transports, not messages.** `latere.ai/x/pkg/email` provides `New(apiKey, domain, region string, smtp *SMTPConfig) (Sender, error)` returning a `Sender` with exactly one method:

```go
Send(ctx context.Context, to, subject, htmlBody string) error
```

Selection stays as auth wrote it: Mailgun when an API key is set and is not `"test"`, SMTP when a host is configured, log-only otherwise — so a local run without credentials prints the mail and never fails. `rejectCRLF` stays inside the package, where header injection is a transport concern.

Templates, copy, and the domain verbs stay in each service. auth keeps `SendCode`/`SendInvitation` as a thin local wrapper over `Send`, with its own `renderCodeEmail`/`renderInvitationEmail` unchanged. The shared package therefore knows nothing about orgs, projects, codes, or accept tokens, and adding a new kind of mail is a change in one service only.

This is the cut that makes the three-repo change small and provable. auth's existing `mailgun_test.go`, `smtp_test.go`, and `otel_transport_test.go` move with the code they test, so the extraction is verifiable as behaviour-preserving rather than asserted to be.

**replichai's invitation has no token and no accept flow.** auth's org invitation mints a token, mails an accept URL, and creates the membership when the link is followed. replichai's does not, and must not be made to: `Add` already created the membership, authorised by project ownership. There is nothing to accept — the mail is an announcement with a link to the project, and the invitee's authorisation is decided on arrival by the same predicate that decides it for every other read. A token here would add a second authority for the same fact.

**Sending is best-effort and never fails the invite.** A mail failure is logged and the request still returns `204`, mirroring `seedCollaborators` (which reports a failure by omission) and the publish path's forge flip (logged, non-fatal). The membership is the durable thing; the mail is a courtesy that can be retried by re-inviting. The alternative — failing the invite when Mailgun is down — would make an outage at a third party look like a bug in membership.

**The mail says what is true after 006q.** It names the inviter, the project, and links to it, and it does not tell the invitee to wait for an admin. With sign-in open, they can sign in immediately; what they may not do is start a session the project cannot pay for. The copy points at the project and says what is true after [011](011-what-a-session-costs.md): sessions started there are paid for out of the project's own credits, so an invitee needs nothing of their own to run one. Landing this spec before 006q means shipping the old sentence and changing it; landing them together is preferred, and the acceptance criteria state which sentence is correct at which point.

**Configuration is new, and enumerated.** replichai gains `MAILGUN_API_KEY`, `MAILGUN_DOMAIN`, `MAILGUN_REGION`, and the `SMTP_*` set, wired in `cmd/server/main.go` beside the other environment reads. `BASE_URL` already exists and is what the project link is built from. Every knob lands in `example.env`, `docker-compose.yml`, `deploy/base/deployment.yaml`, and `deploy/README.md` in the same change — a mail knob that exists only in code is a production incident waiting for its first invite.

**An unconfigured deployment still works.** With no key and no SMTP host, `New` returns the log sender: the invitation is written to the log, the membership is created, nothing errors. Local development and CI need no credentials and no network.

## Acceptance criteria

1. `latere.ai/x/pkg/email` exposes `New` and `Sender.Send`, selects Mailgun → SMTP → log in that order, rejects CRLF in the subject, and carries auth's transport tests unchanged in behaviour.
2. auth compiles and passes its full suite against the shared package, with `SendCode`/`SendInvitation` and their rendered copy unchanged — asserted by the existing tests, not by inspection.
3. replichai builds a `Sender` at startup from the environment; a deployment with no mail configuration gets the log sender and starts normally.
4. `POST /projects/{id}/members` sends the invitation to the added email: subject names the project, body names the inviter and links to `{BASE_URL}/projects/{id}`.
5. A send failure is logged and the response is still `204`, with the membership row present — asserted with a failing sender.
6. An invite that adds nobody new (already a member) does not re-send. Re-inviting a removed member does.
7. The email address is taken from the request body after normalisation, and the inviter's name from the session — never from the body.
8. Every new environment variable appears in `example.env`, `docker-compose.yml`, `deploy/base/deployment.yaml`, and `deploy/README.md`.
9. The invitation copy matches the sign-in rules actually in force. If 006p lands first, the mail and the frontend's invite hint still say the invitee needs a platform invitation from an admin, because they do; [006q](006q-sign-in-is-open-budget-is-not.md) is what makes that sentence false, and removing it is that spec's acceptance criterion, not this one's. Landing them together is preferred precisely so the sentence is written once.

## Out of scope

- **Any other notification.** Session finished, session failed, project published, sign-off requested: all of them are plausible and none of them are this spec. The seam this adds makes them small later.
- **An accept/decline flow.** Membership is created by the owner and removed by the owner. If a decline is ever wanted, it is a spec about consent, not about mail.
- **HTML design.** The mail is a paragraph and a link, matching auth's existing invitation. A template system is a later problem for a later volume of mail.
- **Bounce handling, unsubscribe, delivery status.** Transactional mail to a named collaborator, sent one at a time by a project owner.

# Specs

Designs, and the record of what was built. One spec per tightly-scoped feature; each states the problem, the decisions, the acceptance criteria, and what it deliberately leaves out. This page is the map: what exists today, how it fits, and what is left.

**The table is the record.** A spec's own file carries its status and its argument; the map below carries its row. The paragraphs on this page are the exception, not the pattern: they are kept for shipped specs whose reasoning a later reader needs before touching the code (why the fold is not the drawer, why a moved session needs no second project id, why `repo_name` stopped meaning success). A shipped spec that carries no such lesson needs no paragraph here.

Feasibility questions and scope extensions that are not on the roadmap (multi-study papers, hard-to-replicate instruments, unpublished manuscripts) live in [`BACKLOG.md`](BACKLOG.md), deliberately unsequenced. One of them, [016](016-a-paper-that-is-not-published-yet.md), needed enough argument to get a deferred spec of its own.

## How to read this

A spec is one design: the problem, the decisions and why, testable acceptance
criteria, and what it deliberately leaves out. When it is built, an Outcome
section records what actually shipped and where it diverged.

Start with [Decisions to audit](#decisions-to-audit) for the choices the whole
system rests on, then [the spec map](#the-spec-map) for every spec and its
state, and [What is left](#what-is-left) for what is next.

Each spec's frontmatter carries one status:

| Status | Meaning |
|---|---|
| `draft` | argued, not yet agreed |
| `designed` | agreed, not built |
| `built`, `implemented` | shipped; older specs say implemented, newer ones built. The map's Done and Half done groups say whether all of it shipped |
| `deferred` | waiting, for the reason the spec states |
| `superseded` | replaced by a later spec, and kept under [`.archive/`](.archive/) |

Several specs mention services of the Latere platform that ReplicHAI can run
on: Topos (its agent runtime), Lux (its model gateway), Cella (its sandbox
control plane) and Auth (its identity provider). Latere AI operates the hosted
service at replichai.latere.ai.

**Proposing a spec.** Open an issue first, so the problem is agreed before the
design. Then send a pull request adding `NNN-short-title.md`, numbered one
above the highest in use, with `status: draft` in its frontmatter and a row in
the map's Not built table. `go tool lateregate spec-lint` checks that the tree
and this index agree.

## What replichai is

Give it a paper. It decides what of the paper can be replicated: it resolves the paper's identity, runs an agent that reproduces the machine-runnable inner loop, publishes the reproduction as its own GitHub repository with the agent's commit history, and returns an independent verdict, recording every decision the paper left unwritten. The audit itself never simulates the human study. The goal is the whole study ([050](050-the-outer-loop-simulated.md)): repeat runs measure whether the inner loop is stable enough to build on, and a paper that passes that gate continues to the outer loop, where an agent builds the paper's experiment, simulated participants with a declared population profile take part in it, and the paper's conclusion is checked against what they did, calibrated against human replications. The audit is built; the outer loop is designed and not built.

## Current status

**Built and working today (the engine and the live publish loop):**

```
replichai audit <paper> --publish [--detach] [--invite <login>]
   resolve identity (001)                          internal/paper
   → registry warns on prior reproductions,        internal/registry (003)
     allocates <canonical-slug>-<suffix>
   → create a private repo under open-hai          internal/github
   → mount it into the agent's sandbox             internal/provider (RepoMount → github_repository)
   → the agent commits AND PUSHES it, live         internal/audit (006f, 044a)
replichai finish <owner>/<repo> <paper> --session-ref <ref>
   → reattach, wait, verify the committed tree,    internal/publish (VerifyTree)
     commit replication.json, invite the user      internal/github (PutFile, Invite)
```

This was proven end to end: a real reproduction (`open-hai/short-form-videos-…-wtmk`) with agent-authored history, an independent manifest, and a collaborator invitation. `audit --publish` blocks; `--detach` starts it and prints a `finish` command to run later, since the agent commits independently of the CLI.

**And the same engine on the operator's own machine (045):**

```
replichai audit <paper> --runtime local [--sandbox host|container] [--harness claude-code|codex] [--model best]
   bootstrap stage: the declared packages, into the session's own environment
   → worker stage: the harness, confined to one workspace by srt (or a container)
   → judge stage: the same harness, workspace read-only, verdict to grade/
   → the host verifies the committed tree and pushes it — no forge credential
     ever enters the sandbox
replichai run <project-file> --parallel 4 [--publish] [--resume]
replichai login | project create | push <project-file> --project <id>
```

The local runtime is a peer of `anthropic-managed`, not a fork: it satisfies the same `managed.Runtime` contract and passes the contract tests in `internal/managed/contracttest`, as the memory runtime does. Nothing about it touches the credit ledger; the operator bought that compute from the vendor themselves.

This has been run, which is the reason to believe any of it. A session driven by a real `srt` around a real Claude Code, and a second around a real Codex, on the operator's existing login with no API key: bootstrap, worker, judge, verdict. Roughly a dozen defects sat between the deck as written and that run, and every one of them was a fact about somebody else's program rather than a mistake in the reasoning: a sandbox that refuses a wildcard, a login kept in a keychain, a client that will not trust an injected authority, a shell that needs a scratch directory. [045](045-the-run-happens-on-your-machine.md)'s Outcome carries the list and the lesson.

**Implemented:** the engine: 001 (identity), 002 (publishing + the redistribution gate), 003 (registry), 004 (the Go port), 005 (transcript), 007a (the instrument), 007b/007c (experiments and the synthetic pilot, engine halves); and the application on top of it: 006a (HTTP + SSE), 006b (job model + Postgres), 006c (OIDC login + the fail-closed access gate), 006e (the frontend), 006f/006i (the mount publish, from the CLI and from the server), and everything the numbered specs from 011 up have added to them. **010** provisions production: domain, database, k8s manifests, and a tag-triggered deploy pipeline.

**The app runs end to end**: `docker compose up` (or `make up`) brings up Postgres + the server, which serves the React frontend (006e) and the API. A researcher signs in, submits a paper URL, and watches its phases stream live to `done` behind the real fail-closed gate. Production is provisioned and fail-closed.

**Built:** [006i](006i-server-side-mount-and-publish.md) wires the CLI's live-mount publish into the server `engineRunner`, so a web-submitted session gets a real repository, manifest, and committed transcript rather than being lost with the sandbox. This is the backend half of 006f, with the CLI's finalize extracted into a shared `publish.FinalizeLive`. That shared finalize keeps the manifest identical on both paths, but it was never the whole seam: the two drifted on the artifacts channel anyway, which is 044's second defect, and [044b](044b-the-server-harvests-the-session.md) added the server's own `harvestOutputs` to commit what a session left in its output directory through the same `VerifyTree` gate.

**Built:** [006j](006j-the-new-project-flow.md) makes "New project" a deliberate act: name it, preview each paper link (`POST /resolve`), start several papers at once, and invite collaborators, through a dedicated `POST /projects` (plus `PATCH /projects/{id}` rename).

**Built:** [012](012-a-project-holds-papers.md) separates the two acts 006j fused. A paper becomes a row a project holds (`project_papers`, keyed by the `paper_key` a session already carries), not an argument to starting a run: create records papers and enqueues nothing, and a member starts a session per paper from the project screen. Creating a project stopped being spending, which is what used to refuse an empty wallet for the act of naming a container, so the credit check is asked once, on the Start button, with the project's pot on the same screen. `POST /papers` is retired; four routes under `/projects/{id}` replace it, and `fundNewProject`, `undoProject`, the 422, and the frontend's partial-success apparatus are removed with the enqueue that needed them.

**Built (running the seed phase from inside the app):** [006k](006k-the-platform-admin-console.md) gives the admin a screen (grants, promotions, the platform policy row, per-person credit) instead of `curl` and a redeploy; [011](011-what-a-session-costs.md) spends against the numbers it stores: one check inside `Enqueue`, in the inserting transaction, settled in the transaction that ends the session; [006m](006m-the-signed-in-person.md) renders the name and avatar the IdP already has. [006l](.archive/006l-the-session-quota.md) was the first attempt at that meter and is superseded, kept for the argument 011 inherits.

**Built:** [006n](006n-who-signed-off.md) gives the public sign-off a person: a researcher profile (name, institution, public contact, ORCID, Scholar) kept in settings, prefilled into a step-by-step publish wizard, and **snapshotted onto the project** so a later profile edit never rewrites a published attestation. The signer's own email is derived from the session and stored redacted; attestation v2.

**Built:** [006o](006o-withdrawing-a-publication.md) gives the one-way public flip an end: an owner may withdraw a publication back to private, the forge repository follows, and the retraction is recorded beside the sign-off rather than erasing it. Withdrawal is not deletion (which cascades and stays an admin action), which is why an owner can be trusted with it.

**Built:** [006p](006p-the-invitation-that-arrives.md) sends the project invitation 006f never sent. auth's mail transport moved to a shared `latere.ai/x/pkg/email` (the transports, and since pkg v0.97.0 the HTML layout and its escaping; each service keeps its own copy), and `internal/notify` owns what replichai says to somebody who is not looking at it. The message is an announcement, not an offer: the membership already exists, so there is no token and nothing to accept. Sending is best-effort, because the membership is the durable thing.

**Built:** [006q](006q-sign-in-is-open-budget-is-not.md) splits *may sign in* from *may spend*. Signing in self-grants `TierUser`, a new account arrives with nothing to spend, and the meter, not an admin's guest list, is what protects the credential. Revocation becomes recorded state rather than a delete, because a self-granting sign-in would re-create a deleted row and turn a ban into a logout. With 006p, an invited collaborator now receives mail, follows it, signs in, and sees the project, which nobody could do before.

**Built:** [006r](006r-the-public-profile.md) gives a researcher a handle and a public page at `/u/{handle}`, listing the public work signed under that name, and requires one before a project may be published: asserting a result under your name means the name has to resolve to something. The sign-off card links to it through a field resolved per read and never snapshotted, so 006n's invariant holds: making a profile private later removes the link and changes nothing on the record.

**Built:** [006s](006s-reading-a-session-in-time.md) makes a 771-event transcript navigable: the run's real clock as the spine (proportional phase bands, a histogram of where time was spent, a scrubber that scrolls to a moment), search, a selected-event inspector, and the per-event token usage the server parsed out of `raw.model_usage` and then discarded. A revision of 006h: that spec made the transcript visible, this one makes it readable. Its two corrections matter most: the bar spans the *session*, not its events, because every event is emitted inside `running`; and it weighs *fresh* tokens, not the total, because cache reads are 99% of a real total and measure conversation length rather than work.

**Built:** [013](013-the-address-bar-is-the-state.md) gives every screen a URL. Navigation was one `useState` and the address bar never moved, so a reload started over at the dashboard, no screen could be linked to, and Back left the app. The path is the state now: one `pathOf`/`viewOf` bijection over the `View` union, so a screen cannot exist without an address, and history follows one default: push when the path changes, replace when it does not. The registry and the console live at `/registry` and `/console` because the API owns `/public` and `/admin`; `/p/{id}` and `/u/{handle}` keep the meaning 006p's mail and 006r's pages already published.

**Built:** [015](015-a-verdict-belongs-to-a-session.md) puts a verdict and a repository back on the session that produced them. The project's Artifacts and Verdict tabs read `sessions.find((s) => s.repo_name)`, the newest session anywhere in the project with a repository, and showed it under an address that named the project, so on a project holding two papers the second hid the first and one paper's reproduction was reachable by nothing. That line is 012's split left standing in the two screens that report results; the same root cause named the project after the wrong paper. Both panes moved to `/p/{id}/s/{jobId}/{verdict,artifacts}`, the project's artifacts tab is gone (a project has as many repositories as published sessions), its verdict tab became the read across papers it had implied, and sessions nest under the paper they are runs of. `repo_name` stopped standing in for success: it marks that publishing *began*, and `publishing → failed` is legal.

**Built:** [017](017-the-first-screen.md) gives the product a first screen and an identity. The surface a visitor met was a 440px card holding the character `r`, the name, one sentence and a button, and that `r` was set in a font (`Instrument Serif`) the app has never shipped, so the mark had always rendered in Georgia. The landing is now two columns, the claim and the inner/outer boundary beside the way in, collapsing on a phone to an order that keeps the button reachable; the mark is a drawn SVG of what the product defends, a closed inner ring inside an open one; and `index.html` has a description, a tab icon and a social card, so a link pasted into Slack is no longer a bare URL.

**Built:** [018](018-one-list-of-projects.md) merges "My projects" and "Shared with me" into one list. The screen split a reader's work by a fact they did not choose (whether they had created a project or been invited to it), so finding work started with remembering who had started it, and the two halves disagreed about the same project, because the owned list counted only the sessions *you* had submitted. Ownership is a column now, not a place: `GET /shared` is gone, one query summarizes a project's sessions whoever started them, the owner is named through their profile (never their address; the redaction rule that governs a signer's email governs this too), and the list sorts on a modified time derived from the newest of the project, its papers and its sessions rather than on when a container was opened.

**Built:** [019](019-a-project-can-be-thrown-away.md) lets an owner throw a project away. Nothing in the product removed one before: `Delete` existed in the store and no route had ever called it, because 006o's argument for giving owners withdrawal rested on withdrawal *not* being deletion. Removing is two steps now, and the guards are what make the second one safe. The trash is reversible and a `deleted_at` column, so a project leaves every list without taking its papers, sessions or repositories with it; a running session refuses it, and a public project is withdrawn on the way out with the retraction recorded. The permanent delete is refused while the pot holds credits, because a balance nobody can account for is what the ledger exists to prevent, and it closes the published repositories rather than destroying them, because a reproduction the agent wrote is evidence. The trashed check lives at the three gates every read and write already pass, so the sessions and transcripts under a trashed project close in one edit rather than five.

**Built:** [020](020-download-the-whole-project.md) gives a project one download. The app proxies committed files one at a time because the repositories are usually private, which meant there was no way to get the work out: a project with three sessions and forty files each was a hundred and twenty clicks and a hundred and twenty loose files with no record of which session each came from. `GET /projects/{id}/archive` streams a zip organized by session (`sessions/{job-id}/…`, because 015 established that a repository belongs to the session that produced it and a flat merge would collide two sessions' files). The status code is committed before the walk finishes, so a forge read that fails is recorded in the archive's own `MANIFEST.txt` rather than truncating the download silently. The gate is view rights, not ownership: a collaborator downloads the project they work in, and a reader downloads what was published.

**Built:** [030](030-the-app-on-a-phone.md) makes the app work on a phone. `.shell` was a flex row with `.rail` at a hard 236px and no shell-level media query anywhere in the stylesheet, so a 390px screen got **154px** of content, on all fifteen screens, and the topbar's controls ran to x=579 where nothing could tap them. Below 760px the rail leaves the flow and becomes a drawer over a scrim, opened by a hamburger in the topbar and closed by the scrim, Escape, its own control, or opening a destination. The drawer is deliberately not the fold: `sidebar.ts` holds that a width the operator chose is never overridden by one the window happened to be, so `useSidebar` is untouched, the new `useDrawer` never persists, and the stored fold is neutralized in CSS rather than read in JS. What made this invisible for so long is the second half of the fix's test: `.screen` is `overflow-y: auto`, which computes `overflow-x` to `auto` as well, so the overflow lived in a pane that scrolled sideways and `documentElement.scrollWidth` read clean while nothing was legible. `frontend/test/mobile.test.mjs` therefore asks both questions across every screen.

**Built:** [031](031-the-model-is-a-choice.md) lets whoever starts a session choose the model it runs on. Every managed session this product had ever run ran on Opus at $5/$25 per Mtok, not as a default but as the only value: `audit.WithModelPreference` existed and was called from exactly one place in the tree, a test. The three tiers were already vocabulary (`credit.Preferences`) and already priced (`Card.Covers`), reachable only from a boot log and a CLI report; what was missing was a way to choose. The tier is now a closed typed enum validated at the HTTP boundary. The adapter passes an unrecognised preference straight through as a literal model id, and a model the card cannot name is billed at the highest rates it holds, so an unchecked string was both a request for a model that may not exist and a bill at the top of the card. It is stored as a column rather than consumed at enqueue for a plain reason: `job.Submission` does not outlive `Enqueue`, and the worker that eventually runs the session, possibly a different process on a different pod, sees only the row, so a tier that stopped at the handler would reach no run at all. Both paths take one `auditOptions(j, observe)` that cannot be called without the job, though only the fresh one acts on it: `Resume` rejoins a session whose model was fixed when it was created. Prices come from a new `GET /models` and never from the client, for the reason 011 snapshots the card at all. What this does *not* touch is the gate: `takeReserveTx` reads the reserve with no model in hand, so a cheaper tier lowers burn rate, not what a project must hold to begin. This is asserted explicitly, so nobody later reads the change as having loosened admission.

**Built (money comes in without an admin):** the six specs 022a to 022f add the front door 011 left locked. A person buys credits into their own wallet with a card: a Stripe hosted Checkout session creates the charge, a signed webhook is the one thing that posts the credit, and the ledger dedupes that post on an `external_ref` so a retried delivery cannot double it. The amount credited is the amount paid less a spread an admin sets, which keeps the rate card at vendor prices so a session's cost still reconciles against them. The checkout is created in **USD** (a credit is prepaid USD compute), and no code charges, reads, or converts another currency. Refunds are a negative `adjust` and may take a wallet negative, and a negative wallet **freezes the account** into view-only mode (every write refused `402 account_frozen` except the top-up that thaws it), with a one-shot admin override and a frozen/negative filter on the admin people table, so a chargeback-after-spend cannot quietly keep costing compute. Admin top-up stays as the way a grant or comp enters a wallet.

**Built and deployed:** the project/session rework: 006g (projects as first-class records holding replication sessions, the double-trigger guard, the CLI backfill migrated into three one-session projects) and 006h (the session transcript viewer: trajectory + token usage, usage derived from the transcript for backfilled sessions).

**Built (what the run actually found):** the [021](021-what-the-run-actually-found.md) chain turns a finished session from a folder of markdown into a result somebody reads. [021a](021a-the-findings-as-data.md) makes the findings data (`verdict.json`, its schema in `internal/verdict`, graded by the rubric); [021b](021b-a-verdict-that-reads-like-one.md) renders it as a verdict rather than a file listing; [021c](021c-what-the-authors-released.md) reports what the authors released; [021d](021d-watching-a-session-run.md) is the live session screen; [021e](021e-the-project-rail.md) is the project's own rail: what it holds, what it has spent, whether it can be published.

**Built (the places the app has):** [024](024-settings-is-a-place.md) makes personal settings a destination with its own tabs, and makes the ledger name the project each move went to, durably enough that a deleted project still reads. [028](028-the-registry-opens.md) opens the registry, a public project, and a public session to a reader who is not signed in: nine reads moved outside the gate, with the redaction keyed on the same predicate that already withheld a balance. [029](029-the-landing-is-a-place.md) gives the signed-out reader a real landing page, and [033](033-the-landing-rebuilt-from-the-design.md)/[034](034-the-console-rebuilt-from-the-design.md)/[040](040-the-landing-in-light.md) rebuild the landing and the console from the design, in both themes, ending on the footer the rest of Latere shares.

**Built (the runtime seam and the run that produced nothing):** [041](041-the-runtime-is-not-the-model-vendor.md)'s children put managed execution behind one contract and made a session remember the runtime that owns it (two halves are still short; see the half-done table). [042](042-the-site-a-crawler-can-read.md) makes the site readable by a machine that is not a browser, [043](043-the-registry-names-the-paper.md) makes the registry name the paper it reproduces, and [044](044-a-run-that-produced-nothing.md) makes a run that committed nothing say so instead of reporting success.

**Built (what a project holds and says):** [036](036-a-project-can-start-empty.md) lets a project be created with a title and no papers, dropping the requirement 012 had left in place. [037](037-a-paper-carries-its-bibliography.md) gives a paper its bibliographic record rather than a bare reference string. [038](038-a-project-says-what-it-is-about.md) gives a project a description, and [039](039-a-description-can-be-drafted.md) lets a model draft that description, through `internal/draft`, a single priced call that is deliberately not a `provider.Provider`, against whichever model the deployment configures.

## Architecture at a glance

**The neutral core, the named edges.** The orchestrator (`internal/audit`) and the contracts (`internal/managed`, `internal/provider`) name only roles; the adapters and the composition root (`internal/runtimes/builtin`, `cmd/`) name the vendor, and a test walks the import graph to keep it that way. A `memory` runtime runs nothing, so the whole orchestrator is testable offline.

**The package map:**

| Package | Role |
|---|---|
| `internal/paper` | resolve a reference to a stable identity + slug (001) |
| `internal/method` | the method — instructions, rubric, task template, experiment doctrine — and its digest |
| `internal/instrument` | the `instrument.json` schema and its validation (007a) |
| `internal/verdict` | the `verdict.json` schema — the findings as data (021a) |
| `internal/provider` | the original backend contract; its neutral half is still everyone's event vocabulary (041) |
| `internal/managed` | the vendor-neutral managed-session contract and the controller that drives one (041a, 041b) |
| `internal/runtimes` | the explicit runtime catalog and durable router — no registry, no `init` (041, 041c) |
| `internal/audit` | the orchestrator: run an audit, optionally into a mounted repo |
| `internal/fetch` | the one outbound fetcher for user-supplied URLs |
| `internal/batch` | one project file: many papers, several runs each, one copy of each paper and its artifacts for all its runs, in parallel and resumable (045h, 046) |
| `internal/cloud` | signing in from the terminal, and the API client a local session files its results through (045i, 045k) |
| `internal/experiment` | run a reproduction's declared analysis on new data (007b); never imported by `audit` |
| `internal/publish` | the manifest, the redistribution gate, naming, the filesystem forge |
| `internal/github` | the small GitHub client publishing and the registry need |
| `internal/registry` | the derived registry: relate reproductions of one paper (003) |
| `internal/job` | the job lifecycle: the state machine, the store seam, the worker (006b) |
| `internal/project` | projects, the papers they hold, and their members (006g, 012, 035) |
| `internal/access` | the authorization model: grants, tiers, project membership, seed-and-bind (006c) |
| `internal/platform` | the policy an admin tunes at runtime: the settings row — start reserve, overdraft floor, rate card (006k, 011) |
| `internal/credit` | the ledger: wallets, project pots, holds and charges, in micro-USD (011) |
| `latere.ai/x/pay` | the card-processor seam, its Stripe adapter and the ledger's tables, shared across Latere products and composed in `cmd/server` (022a, 022b) |
| `internal/profile` | the self-asserted researcher identity a public sign-off is published under (006n) |
| `internal/notify` | what the platform says to somebody who is not looking at it (006p) |
| `internal/draft` | one short model call — a prompt in, a paragraph and a token count out (039) |
| `internal/httpapi` | the HTTP server: submit, status, SSE, read surfaces, the gate seam (006a) |
| `internal/webauth` | the OIDC login, session cookie, gate policy, and dev bypass (006c) |
| `internal/seo` | the head the server writes, the sitemap, and the addresses Go knows (042) |
| `internal/spa` | embeds and serves the built frontend as the app shell (006e) |
| `internal/doccheck` | tests that `example.env`, `docs/cli.md` and `docs/architecture.md` match the code |
| `internal/oteltest` | a recording tracer for tests that check the spans instrumentation produces |
| `frontend/` | the researcher UI — bun + React, i18n scaffolding (English only today) (006e) |
| `cmd/replichai` | the CLI: `audit`, `run`, `resolve`, `publish`, `finish`, `login`, `whoami`, `logout`, `org`, `sessions`, `project`, `push`, `providers`, `rates` |
| `cmd/server` | the web app composition root: wires the engine + stores + gate, runs the worker + HTTP server (006a/b) |
| `deploy/` + `.github/` | k8s kustomize (prod/staging) and the CI/release pipeline (010) |

**The trust model: a reproduction is a claim / evidence / verdict triad**, and who authors each is deliberate: the subject of an audit never writes its own grade.

| Layer | Author | Artifact |
|---|---|---|
| Claim | the agent, in the sandbox | the reproduction — code, history, `REPRODUCIBILITY.md` |
| Evidence | the backend's event log, fetched by the system | the transcript (005) |
| Verdict | the system, outside the sandbox | `replication.json` |

**Two publish paths, on purpose.** The *mount* path (006f) is the primary one: the agent commits into a live GitHub repo, and the gate becomes a post-run *verifier* (`VerifyTree`, a blocklist of third-party data, because an agent-authored repo is a full project, not a fixed file set). The *filesystem* path (002: `Forge`/`FSForge`, `Classify` allow-list, the `publish` CLI command) stays as the offline, no-backend way to exercise publishing.

## Decisions to audit

- **The project is the tenant.** No org in the product; each user owns private projects (owner + invited contributors), private by default, made public by the owner. One backend credential pays for every hosted run. The app owns the ownership model; artifacts live in a `Store` (the GitHub forge for the MVP, [drive](https://drive.latere.ai) later, behind one seam: 008). (006)
- **The registry is derived, not stored.** Read the org's manifests; no second source of truth. The database holds the app's own state, never the set of published reproductions. (003, 006b)
- **Ownership by invitation, never delegation.** The Latere OIDC login proves identity only; the org invites the GitHub login the request names, and ownership comes from that invitation rather than from anything the platform stored about the account. The service never holds a user's repo credentials. (006f, 006c)
- **The redistribution gate fails closed.** Third-party data is never published; the method keeps downloads out of the mounted repo, and the verifier bars a leaked repo from ever going public. (002, 006f)
- **Never simulate the outer loop.** Outer-loop execution is a downstream engine, `verdict`/`rate` stay audit-only, a synthetic cohort is labeled indelibly. Regression-tested in `evals/case-01`. (007)
- **Open sign-in, metered spending.** Anyone signs in with a latere account and is granted `user`; what limits them is money, not a guest list, and they arrive with none. The gate stays fail-closed and per-request. (006c, 006q, 011)

**006f is built and deployed:** the per-project collaboration API, the owner's public flip gated by a recorded responsibility sign-off, the public zone, and withdrawal (006o) all shipped, and 006i wired the server-side mount-and-publish 006f had left open. What remains of 006f is the **two-token split**, a fine-grained, per-repo mount token distinct from the org credential, which is now **009**. Its first two commits shipped: the transcript-leak gate (a foreign secret in the transcript now bars the flip under a `secret_in_transcript` review, closing the 005 divergence) and a GitHub App credential that moves every *server-side* operation off the org PAT, with installation-token refresh. Its third commit shipped for runs on the operator's machine and waits for the mount; [What is left](#what-is-left) says why.

## The spec map

Three tables, by how finished the work is rather than by number. A spec's number tells you when it was written; this tells you what you can rely on. Within each table the order is numeric.

### Done (91)

Built, and every acceptance criterion it set itself holds.

| # | Spec | Status | Depends on |
|---|---|---|---|
| 001 | [Paper identity and slugs](001-paper-identity-and-slugs.md) | implemented | — |
| 002 | [Publishing reproductions](002-publishing-reproductions.md) | implemented | 001 |
| 003 | [The reproduction registry](003-reproduction-registry.md) | implemented | 001, 002 |
| 004 | [Port the core to Go](004-go-port.md) | implemented; its Python half deleted | — |
| 005 | [Capture and publish the reproduction transcript](005-transcript.md) | implemented | 002, 006f |
| 006a | [HTTP API and streaming](006a-http-api-and-streaming.md) | built | 006b, (003, 005) |
| 006b | [Job model and persistence](006b-job-model-and-persistence.md) | built | 002, 004 |
| 006c | [Identity, tiers, and the access gate](006c-identity-and-access.md) | implemented | 006b |
| 006e | [Frontend](006e-frontend.md) | implemented | 006a |
| 006g | [Projects and replication sessions](006g-projects-and-replication-sessions.md) | implemented, deployed | 006b, 006c |
| 006h | [Session transcript viewer](006h-session-transcript-viewer.md) | implemented, deployed | 006g, 005, 003 |
| 006i | [Server-side mount and publish](006i-server-side-mount-and-publish.md) | built | 006f, 006b |
| 006j | [The New-project flow](006j-the-new-project-flow.md) | built | 006g, 006c |
| 006k | [The platform admin console](006k-the-platform-admin-console.md) | built; its policy row reshaped by 011 | 006c, 006b |
| 006l | [The session quota](.archive/006l-the-session-quota.md) | **superseded by 011** | 006k, 006b, 006g, 006c |
| 006m | [The signed-in person](006m-the-signed-in-person.md) | built | 006c |
| 006n | [Who signed off](006n-who-signed-off.md) | built | 006f, 006g |
| 006o | [Withdrawing a publication](006o-withdrawing-a-publication.md) | built | 006f, 006n |
| 006p | [The invitation that arrives](006p-the-invitation-that-arrives.md) | built | 006f, 006c |
| 006q | [Sign-in is open, budget is not](006q-sign-in-is-open-budget-is-not.md) | built; half superseded by 011 | 006c, 006l, 006k |
| 006r | [The public profile](006r-the-public-profile.md) | built | 006n, 006f |
| 006s | [Reading a session in time](006s-reading-a-session-in-time.md) | built | 006h, 006g |
| 007a | [The study protocol as an artifact](007a-the-study-protocol-as-an-artifact.md) | implemented | 004 |
| 010 | [Deployment and release](010-deployment-and-release.md) | built | 006a, 006b, 006c |
| 011 | [What a session costs](011-what-a-session-costs.md) | built | 006b, 006c, 006g, 006k, 006h |
| 012 | [A project holds papers](012-a-project-holds-papers.md) | built | 006g, 006j, 006c, 011, 001 |
| 013 | [The address bar is the state](013-the-address-bar-is-the-state.md) | built | 006e, 006g |
| 014a | [Managed runs close truthfully](014a-managed-runs-close-truthfully.md) | built | 004, 006b, 011 |
| 014b | [Job-state writes are required](014b-job-state-writes-are-required.md) | built | 006b, 011, 014a |
| 014c | [Publication requires evidence](014c-publication-requires-evidence.md) | built | 002, 005, 006i, 009 |
| 014d | [An auth outage is not sign-out](014d-an-auth-outage-is-not-sign-out.md) | built | 006c, 006e, 013 |
| 015 | [A verdict belongs to a session](015-a-verdict-belongs-to-a-session.md) | built | 012, 013, 006f, 006s |
| 017 | [The first screen](017-the-first-screen.md) | built | 006e, 006q, 013 |
| 018 | [One list of projects](018-one-list-of-projects.md) | built | 006g, 006f, 006r, 013 |
| 019 | [A project can be thrown away](019-a-project-can-be-thrown-away.md) | built | 006g, 006o, 011, 013, 018 |
| 020 | [Download the whole project](020-download-the-whole-project.md) | built | 006f, 005, 015, 018 |
| 021 | [What the run actually found](021-what-the-run-actually-found.md) | built | 002, 005, 007a, 011, 015, 018 |
| 021a | [The findings as data](021a-the-findings-as-data.md) | built | 021, 007a, 002, 006i |
| 021b | [A verdict that reads like one](021b-a-verdict-that-reads-like-one.md) | built | 021a, 015, 006s |
| 021c | [What the authors released](021c-what-the-authors-released.md) | built | 021a, 015, 002 |
| 021d | [Watching a session run](021d-watching-a-session-run.md) | built (revised down) | 006a, 006s, 005, 011 |
| 021e | [The project rail](021e-the-project-rail.md) | built | 021a, 021d, 018, 006n, 011 |
| 022a | [The idempotent paid top-up](022a-the-idempotent-paid-topup.md) | built | 011 |
| 022b | [Checkout and the webhook](022b-checkout-and-the-webhook.md) | built | 022a |
| 022c | [Refunds and the solvency read](022c-refunds-and-the-solvency-read.md) | built | 022b |
| 022d | [The freeze, server-side](022d-a-negative-wallet-is-frozen.md) | built | 022c |
| 022e | [Buying credits](022e-buying-credits.md) | built | 022b |
| 022f | [The frozen account, in the UI](022f-the-frozen-account-in-the-ui.md) | built | 022c, 022d, 022e |
| 024 | [Settings is a place, and the ledger names its projects](024-settings-is-a-place.md) | built | 013, 011, 018, 019 |
| 024a | [Settings as a destination, with sub-tabs](024a-settings-as-a-destination.md) | built | 013 |
| 024b | [The ledger names its project](024b-the-ledger-names-its-project.md) | built | 024a, 011, 019 |
| 028 | [The registry opens](028-the-registry-opens.md) | built | 006f, 006r, 013, 017, 018, 021, 011 |
| 029 | [The landing is a place](029-the-landing-is-a-place.md) | built | 028, 017, 013, 006q |
| 030 | [The app on a phone](030-the-app-on-a-phone.md) | built | 006e, 013, 017 |
| 031 | [The model is a choice](031-the-model-is-a-choice.md) | built | 011, 006b, 012, 006e |
| 032 | [A session can be thrown away](032-a-session-can-be-thrown-away.md) | built | 006b, 006f, 019, 021d |
| 033 | [The landing, rebuilt from the design](033-the-landing-rebuilt-from-the-design.md) | built | 029, 028, 006q, 011 |
| 034 | [The console, rebuilt from the design](034-the-console-rebuilt-from-the-design.md) | built | 033, 030, 018, 021e, 020 |
| 035 | [A paper can move to another project](035-a-paper-can-move-to-another-project.md) | built | 012, 011, 015, 019, 021e, 032 |
| 036 | [A project can start empty](036-a-project-can-start-empty.md) | built | 012, 006j, 035 |
| 037 | [A paper carries its bibliography](037-a-paper-carries-its-bibliography.md) | built | 001, 012, 035 |
| 038 | [A project says what it is about](038-a-project-says-what-it-is-about.md) | built | 018, 006j, 028 |
| 039 | [A description can be drafted](039-a-description-can-be-drafted.md) | built | 038, 037, 011, 006k, 031 |
| 040 | [The landing in light, and the shared footer](040-the-landing-in-light.md) | built | 033 |
| 041a | [One managed-session contract](041a-one-managed-session-contract.md) | implemented | 041, 004, 005, 031 |
| 041b | [One managed-session lifecycle](041b-one-managed-session-lifecycle.md) | implemented | 041a, 005, 006f, 014a |
| 041c | [A session remembers its runtime](041c-a-session-remembers-its-runtime.md) | implemented | 041a, 041b, 006b, 031, 039 |
| 042 | [The site a machine that is not a browser can read](042-the-site-a-crawler-can-read.md) | built | 013, 028, 006r |
| 042a | [The head the server writes](042a-the-head-the-server-writes.md) | built | 042 |
| 042b | [robots.txt, and a sitemap of the public zone](042b-robots-and-the-sitemap.md) | built | 042a |
| 042c | [An address that does not exist](042c-an-address-that-does-not-exist.md) | built | 042a, 013 |
| 043 | [The registry names the paper it reproduces](043-the-registry-names-the-paper.md) | built | 037, 028, 033 |
| 044 | [A run that produced nothing must not report success](044-a-run-that-produced-nothing.md) | built | 005, 006f, 006i, 009, 021a, 041e |
| 044a | [The mount is pushed, not just committed](044a-the-mount-is-pushed.md) | built | 044, 006f |
| 044b | [The server harvests what the session produced](044b-the-server-harvests-the-session.md) | built | 044, 006i, 009 |
| 044c | [A lease says the driver is alive](044c-a-lease-says-it-is-alive.md) | built | 044, 006b, 041b |
| 044d | [An empty repository says so](044d-an-empty-repository-says-so.md) | built | 044, 021a, 044b |
| 045 | [The run happens on your machine](045-the-run-happens-on-your-machine.md) | built (twelve children) | 041a–041c, 044, 009, 006b, 011 |
| 045a | [The sandbox is a seam](045a-the-sandbox-is-a-seam.md) | built | 041a, 041b, 041c |
| 045b | [The host sandbox](045b-the-host-sandbox.md) | built | 045a |
| 045c | [The container sandbox](045c-the-container-sandbox.md) | built | 045a, 045b |
| 045d | [The harness inside the sandbox](045d-the-harness-inside-the-sandbox.md) | built | 045a, 045b, 005 |
| 045e | [The model is an endpoint](045e-the-model-is-an-endpoint.md) | built | 045a, 045d, 041d, 031, 011 |
| 045f | [The grader cannot edit what it grades](045f-the-grader-cannot-edit-what-it-grades.md) | built | 045a, 045b, 045d, 002 |
| 045g | [The host pushes](045g-the-host-pushes.md) | built | 045a, 006f, 044a, 044b, 044d, 009 |
| 045h | [Many papers at once](045h-many-papers-at-once.md) | built | 045a, 045g, 003, 012 |
| 045i | [Signing in from the terminal](045i-signing-in-from-the-terminal.md) | built | 006c, 014d |
| 045j | [A bearer the API accepts](045j-a-bearer-the-api-accepts.md) | built | 045i, 006a, 006c |
| 045k | [A local session reaches the cloud](045k-a-local-session-reaches-the-cloud.md) | built | 045j, 045g, 006b, 012, 015, 011, 005 |
| 045l | [The repository comes from the platform](045l-the-repository-comes-from-the-platform.md) | built | 045g, 045j, 045k, 009, 006f, 003 |
| 047 | [Set like the work it audits](047-set-like-the-work-it-audits.md) | built | 017, 029, 030, 033, 034, 040 |

### Half done (13)

Shipped and in use, but each is knowingly short of what its own spec asked for. The status column says which part is missing; read these before assuming the spec describes the code.

| # | Spec | Status | Depends on |
|---|---|---|---|
| 006 | [The web application](006-web-application.md) | built through its children | 002, (003, 005) |
| 006f | [Taking a reproduction out](006f-taking-a-reproduction-out.md) | built; hardening in 009 | 002, 004, 006g, (006c) |
| 007b | [Experiments over instruments](007b-experiments-over-instruments.md) | engine built | 007a, 006f |
| 007c | [The synthetic pilot](007c-the-synthetic-pilot.md) | engine built | 007b |
| 009 | [Secret containment when the agent commits live](009-secret-containment-when-the-agent-commits-live.md) | commits 1–2 shipped; 3 shipped for local runs (045l), deferred for the mount | 002, 005, 006f, 006i |
| 041 | [The runtime is not the model vendor](041-the-runtime-is-not-the-model-vendor.md) | normalization built; adapters designed | 004, 005, 006b, 011, 014a, 031 |
| 041d | [Models and meters name their source](041d-models-and-meters-name-their-source.md) | implemented; meter declaration diverged | 041a, 041c, 011, 031, 006s, 039 |
| 041e | [Anthropic conforms to the session contract](041e-anthropic-conforms-to-the-session-contract.md) | implemented; removal half done | 041a–041d |
| 046 | [One run is one draw](046-one-run-is-one-draw.md) | runs per paper, findings schema 2, the run record, the first graded attempt kept, one paper copy, artifact snapshots and network profile per study, and the in-loop schema check built; system-decided matches and the study view designed | 045h, 021a, 003, 045f, 044, 005 |
| 041g | [Latere runs a session](041g-latere-runs-a-session.md) | built: the runtime, graded through a grader thread, on the command line against the live platform; studies wait on Topos read-only inputs | 041e, Topos/Lux/Cella/Auth |
| 048 | [The runtime is a choice](048-the-runtime-is-a-choice.md) | built: runtime and model on the command line and in the app's picker, person-paid jobs, kept sign-ins; the app offers Latere since Latere Auth allows it | 041c, 041d, 041g, 031, 011 |
| 049 | [The work is kept as it runs](049-the-work-is-kept-as-it-runs.md) | built for the command line: snapshots to the output directory and, for a hosted session that publishes, to its repository; the server's snapshots and the platform's saving during a turn designed | 041, 041b, 041g, 044, 045g, 009 |
| 051 | [A session names its environment](051-a-session-names-its-environment.md) | built for `audit` and `run` on `latere` and `anthropic-managed`; choosing it in the app, or from the paper, not built | 041a, 041g, 046 |

### Not built (14)

Designed, deferred, or still an argument. Nothing here is in the product; [What is left](#what-is-left) says why each one is waiting.

| # | Spec | Status | Depends on |
|---|---|---|---|
| 006d | [PDF ingestion](006d-pdf-ingestion.md) | draft | 001, 006a |
| 007 | [From reproduction to replication](007-from-reproduction-to-replication.md) | draft | 002, 004, 006f |
| 007d | [Hosting a study](007d-hosting-a-study.md) | draft | 007b, 006a–006c, 006e |
| 008 | [Drive as the artifact data plane](008-drive-data-plane.md) | deferred | 006 (MVP first) |
| 016 | [A paper that is not published yet](016-a-paper-that-is-not-published-yet.md) | deferred (ideation) | 001, 006d, 012, 007b, 008 |
| 023 | [The outer loop, as a product](023-the-outer-loop-as-a-product.md) | draft | 007b, 007c, 006b, 015 |
| 023a | [Experiments persisted and dispatched](023a-experiments-persisted-and-dispatched.md) | draft | 007b, 007c, 006b, 015 |
| 023b | [The experiment HTTP surface](023b-the-experiment-http-surface.md) | draft | 023a, 006a, 006c |
| 023c | [Running an experiment from the app](023c-running-an-experiment-from-the-app.md) | draft | 023b, 006e, 013, 021 |
| 023d | [Ground truth for a cohort](023d-ground-truth-for-a-cohort.md) | draft | 023c, 007c |
| 050 | [The outer loop, simulated](050-the-outer-loop-simulated.md) | draft, for review: the four stages, the gate, the experiment the agent builds, simulated participants with population profiles, calibration, the benchmark | 046, 007a, 007b, 007c, 007d |
| 026 | [The people table at scale](026-the-people-table-at-scale.md) | designed | 006k, 022f, 011 |
| 041f | [OpenAI Responses runs a session](041f-openai-responses-runs-a-session.md) | designed | 041e |
| 045m | [The role reaches the agent](045m-the-role-reaches-the-agent.md) | draft | 045d, 045a, 005 |

**Built:** [044](044-a-run-that-produced-nothing.md) is the record of the run that cost $2.21, took twenty minutes, and left a repository holding nothing but the five commits the system wrote itself. Four defects, each independently fatal, and each ordinary. The agent committed five times into the mount and never pushed, and a tool result in its own transcript says so explicitly; the mount protocol asked for a push in a subordinate clause and the rubric graded eight criteria, none of which was *did this leave the sandbox*. The agent had also copied its deliverable, `verdict.json` included, into the output directory, where the CLI would have harvested it and the server, which binds `evidence.Artifacts` and reads only `evidence.Events`, discarded it seconds before releasing the sandbox holding the only other copy. Meanwhile a second replica adopted the still-healthy run, because the lease is refreshed only by an observation and the controller emits one only when something *changed*, so five quiet minutes read as a dead process, and the two drivers' finalizes raced, the loser's `422` recording `failed` over the winner's success. The session then reported *the run did not write verdict.json*, which is true, and describes a findings problem where the truth was an empty repository. Its four children fix them one each: [044a](044a-the-mount-is-pushed.md) makes the push its own step and a graded criterion, [044b](044b-the-server-harvests-the-session.md) commits the outputs through the same `VerifyTree` gate the mount has, [044c](044c-a-lease-says-it-is-alive.md) heartbeats the jobs a worker drives and yields the one it has lost, and [044d](044d-an-empty-repository-says-so.md) makes finalize say which of the two things went wrong. 044a and 044b are both needed and fail differently: prevention that depends on the agent's discipline, and recovery that does not.

**Built:** [042](042-the-site-a-crawler-can-read.md) makes the site readable by a machine that is not a browser. Every address the app serves arrived at a crawler and a link unfurler as one fixed head, so every link anyone had shared to a published reproduction previewed as the homepage, `robots.txt` and `sitemap.xml` answered with the React shell, and `/this-page-does-not-exist` answered 200 with the dashboard under a corrected URL. Three children fix it without an SSR rewrite: [042a](042a-the-head-the-server-writes.md) resolves the public addresses in Go and substitutes a marked region of `index.html` before the shell goes out, keyed on whether the record is public rather than on who is asking, with the client keeping the tab current from the second screen onwards; [042b](042b-robots-and-the-sitemap.md) adds a static robots file and a sitemap generated from `ListPublic`, whose entries are the same strings as the pages' canonicals by construction; [042c](042c-an-address-that-does-not-exist.md) teaches Go the route shapes (a second copy of routes.ts, guarded by a test that reads routes.ts) so a miss is a real 404 carrying the app, and the operator screens carry `noindex`. The structured data is metadata the records already hold: a paper's DOI and bibliography (037), a signer's ORCID (006n).

**Built:** [035](035-a-paper-can-move-to-another-project.md) lets a paper be refiled under another project, and takes its replications with it. A paper was filed once and could never be filed anywhere else: `project_papers` is keyed `(project_id, paper_key)` and the only writes were an insert, a title update and a delete. The one correction available (remove it, add it to the right project) stopped working exactly when somebody noticed the mistake, because 012 refuses to remove a paper that has been run. What shipped is one `MovePaper` across two tables in a single transaction, through the seam the job store already uses to reach the credit ledger, so `project` still never imports `job`; and a store wired without that seam refuses outright rather than moving a paper and stranding its runs. Terminal sessions only, for 032's reason and one more: `jobs_active_paper_project` is a partial unique index on `(project_id, paper_key)`, and the upstream `paper_exists` check reads a different table and cannot see a live row in the destination. The money does not move. Every charge stays under the pot that made it, which needs no new column because a moved session is already settled, so the design's first draft, a second project id on the job row, turned out to be answering a question the ledger already answers once. The 30-day spend figure does follow the paper, deliberately: the rail asks what a project has been costing lately, and after a reorganisation those runs are the destination's work.

**Built:** [032](032-a-session-can-be-thrown-away.md) lets an owner delete one session, which is what makes a project publishable again. The publish gate blocks on a single predicate and never reads the phase (a session with a repository and no manifest), and `repo_name` is set the moment publishing *begins*, so a cancel, a publish error, or a restart with no live run to rejoin left a `failed` row holding a repository that would never gain a manifest. That row blocked its project forever: it could not be finished (`PhaseFailed: {}` is terminal and `CanTransition` is the single writer no store bypasses), could not be removed (the only deletion in the job store was 019's purge cascade, which named "deleting one session" in its own *Not in scope*), and could not be worked around, because a re-run opens a second session while the first keeps its repository. The owner met a refusal whose only offer was *Open session*, landing on a transcript that does not mention publishing. What shipped is `jobs.deleted_at` and `DELETE /jobs/{id}`, terminal sessions only: a session in flight raises the *identical* blocker as a dead one, so without that guard the delete on a blocker row would have killed a publish that was merely slow. The repository is closed, not destroyed, through 019's own `closeRepos`: what goes away is the project's claim on the run, not the run. Four read paths hide a deleted session and one deliberately does not: `SpendSince` keeps counting it, because the ledger still holds the charge and a spend figure that dropped on delete would put two totals in front of the same owner.

## The three tracks

**The engine (001–005): done.** 001 decides what a paper *is*; 002 decides where a reproduction *goes* and gates what may be republished; 003 relates repeat reproductions of one paper by identity; 004 is the record of why the core is Go. 005 closed the evidence layer: the agent's full session transcript is fetched and committed, by the system, in the same finalize step as the manifest. 044 then closed the gap underneath it: when the agent commits without pushing, the system harvests the session's outputs into the repository through the same redistribution filter, and a repository that still holds nothing records that rather than reporting success. So a reproduction is claim + evidence + verdict, or it is explicitly marked empty.

**The web application (006): built, deployed, and taking money.** A service that wraps the engine: sign in, put papers in a project, start a session per paper, watch it stream, read the verdict, take the repository out, publish it under your name, and pay for the compute it used. 006 itself is not open work: it is the container, and what it argued for now lives in nineteen children (006a–006s) and in the numbered specs from 011 up, each carrying its own status. The one child still unbuilt is **006d**, dropping a PDF to submit.

Sign-in is open ([006q](006q-sign-in-is-open-budget-is-not.md)): anyone with a Latere account may sign in and is granted the ordinary user tier. What limits them is the meter, not a guest list, and they arrive with nothing to spend until they buy credits (022a to 022f) or an admin grants them.

**Replication (007): the track past the inner loop.** It turns a reproduction into a re-runnable **instrument** (the study protocol, the analysis entrypoint, and a servability verdict, declared as data: **007a**), then generalizes the unit of work: an **experiment** binds one instrument to one **data source**, of which there are four. The paper's released data gives re-analysis; a researcher's uploaded dataset gives a new finding on the paper's instrument (**007b**); an LLM cohort gives a *pilot* that tests the apparatus and never becomes evidence about people (**007c**); real participants, through a page the platform hosts, give re-observation (**007d**). One generalization, four bindings: "run it on my own data" is a row in a table, not a feature of its own.

What 007 does **not** do is touch the audit: the never-simulate-the-outer-loop boundary is a rubric failure condition of the audit. It was also the product's thesis until [050](050-the-outer-loop-simulated.md), which keeps it for the audit and makes a simulated, calibrated outer loop the product's goal. 007 also revised **006b** (a job runs an audit *or* an experiment) and **006c** (project membership beside the tiers), written before either was built, because afterwards it would have been a migration.

007a is built and every audit writes its `instrument.json`. The engine halves of 007b/007c are built and nothing user-facing reaches them. Within 007 the order inverted how the capabilities were first asked for: the synthetic pilot (007c) came third, because a cohort is only checkable where the paper released real data to check it against.

## What is left

Nothing on this list blocks the loop the product exists for: a paper goes in, a graded reproduction with its transcript comes out, and somebody pays for it. These are the next things worth building, and each one names why it is not built yet.

**Measuring a paper with many runs ([046](046-one-run-is-one-draw.md)).** Forty-nine runs of three papers showed that one run is one draw, and that the agent system, the network and the agent's own labels moved the result more than the paper did (a 49-run pilot in September 2026). Runs per paper, a findings schema the agents' real output fits, and the run record are built: every manifest now says which run it was, what it ran on and how it ended, and a run that never started leaves one too. So are the first graded attempt, kept for every run as round 0's findings, with grading possible with no revision at all; the same inputs for every run of a study, which are one copy of each paper, a snapshot of each artifact the project lists for it, and one named network profile, all on the run's record; and a findings file the system cannot read never passing a grading round on the local runtime. What is left is the system rather than the agent deciding whether a number matched, blockers that name their cause, and the study view. What a study measures is decided: per-claim reproducibility given the released materials, with the environment recorded and varied as a facet.

**The outer loop as a product ([023](023-the-outer-loop-as-a-product.md), four children).** The experiment engine and the `KindExperiment` job seam exist; the Postgres stores, the routes, and the screen do not. 023a/023b/023c ship the two data sources that need only live input: `synthetic:generated` and `uploaded`. This is the largest unbuilt thing on the roadmap and the only one that moves the product past the inner loop. Its parent [007](007-from-reproduction-to-replication.md) stays a draft on purpose: it is the argument, and the 023 chain is what gets built from it.

**What running on your own machine leaves open ([045](045-the-run-happens-on-your-machine.md)).** A local run's evidence is not uploaded to the app; it stays in the pushed repository. No harness image is published, so `--sandbox container` needs one the operator built. And no screen labels an imported session as one that ran on somebody's own machine.

**The runtime seam ([041](041-the-runtime-is-not-the-model-vendor.md)).** 041a–041e normalize managed execution behind one contract, make a session remember its runtime, and qualify models and meters. Two halves are knowingly short. The Anthropic adapter was never built at `internal/managed/anthropic`: the SDK stays in `internal/provider/anthropic` behind `managed/legacy`, and the old resource contract still stands in `internal/provider`, used by `managed/legacy` and by `internal/audit`'s older `Auditor`, which only tests construct, so 041e's criteria 4 and 8 are open. And meters are not declared in capabilities. `Capabilities.BillableMeters` was deleted because no runtime ever set it and no caller ever read it; `Capabilities` carries no json tags either, so there was no wire contract to break. Deleting it did not regress anything; it made visible that 041d's criterion 4, the enqueue-time preflight over declared meters, had never been met. The two alternative adapters (041f, 041g) remain design-only.

**Built but unreachable.** `internal/experiment` is roughly 1,800 lines of non-test Go that no binary reaches. Its only importer in the tree is `internal/audit/firewall_test.go`, whose entire purpose is to assert that nothing imports it, and `job.KindExperiment` is declared without a single non-test reference. The engine halves of 007b and 007c are real and tested; what is missing is the [023](023-the-outer-loop-as-a-product.md) chain that would give them an entry point. This is the largest gap between what the specs say is built and what a running server can do.

**Blocked on corpus, not on code.** [023d](023d-ground-truth-for-a-cohort.md) (a cohort with ground truth) and [007d](007d-hosting-a-study.md) (hosting a study for real participants) wait on papers, not on packages: a census taken while writing 023 read every published `instrument.json` and found 0/26 servable components and 0/3 papers whose released data is held. Nothing in the tree recomputes that number, so treat it as a reading with a date on it rather than a standing fact. Until the golden set gains a fully servable study, neither can be checked against anything. The same gap is recorded in [`BACKLOG.md`](BACKLOG.md) item 3.

**Deferred on a hard constraint.** [009](009-secret-containment-when-the-agent-commits-live.md)'s third commit, a per-repo token replacing the org PAT, shipped for runs on the operator's machine ([045l](045l-the-repository-comes-from-the-platform.md)), where the token lives for one push. For the hosted mount, which holds its credential for the whole run inside the sandbox, it cannot ship while a GitHub App installation token lives at most an hour and a real run took 53 minutes. It needs runs bounded under the hour or a backend that can refresh a mount credential mid-session.

**Deferred on scale.** [026](026-the-people-table-at-scale.md) makes the admin people table hold thousands of accounts. The current surface is fine at today's numbers; the spec exists so the fix is known before the numbers force it.

**Deferred by choice.** [006d](006d-pdf-ingestion.md) (drop a PDF to submit), [008](008-drive-data-plane.md) (drive as the artifact data plane, behind the `Store` seam), and [016](016-a-paper-that-is-not-published-yet.md) (a manuscript with no public identity, at ideation level). Still owed and not yet specced: recovering a negative project *pot*; 022a to 022f closed negative wallets, not overrun pots.

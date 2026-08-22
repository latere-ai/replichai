# 010 — Deployment and release

**Its safety premise has moved on.** This spec was written while the login was still being built, so "prod admits nobody" was the load-bearing claim. [006c](006c-identity-and-access.md) shipped the OIDC front door and [006q](006q-sign-in-is-open-budget-is-not.md) made sign-in open — the meter, not a guest list, is the limit, and the release smoke asserts the gate refuses against `/projects` rather than the retired `/reproductions`. Two out-of-scope items also fell: the pipeline builds the frontend, and prod runs two replicas behind a PodDisruptionBudget. The manifests carry secrets this spec never named — Stripe ([022b](022b-checkout-and-the-webhook.md)), the GitHub App ([009](009-secret-containment-when-the-agent-commits-live.md)), and mail ([006p](006p-the-invitation-that-arrives.md)).

**Status:** built (prod infra provisioned; deploy pipeline live)
**Scope:** how replichai runs in production — the Kubernetes manifests, the terraform-provisioned domain and database, and the GitHub Actions release pipeline — and the operational decisions that keep a public deployment safe while the login is still being built.
**Depends on:** [006a](006a-http-api-and-streaming.md)/[006b](006b-job-model-and-persistence.md) (the server and its Postgres store), [006c](006c-identity-and-access.md) (the fail-closed gate; its login front door is the one piece still open).

## Problem

`cmd/server` runs the web app, and `docker compose` runs it locally. Production is a different shape: a shared Kubernetes cluster, a shared Postgres, a public domain, one backend credential, and a deploy that must be repeatable and not hand-run. And it has to be safe *before* the OIDC login exists — a public URL with no sign-in must admit nobody, not everybody.

## Decisions

**One production environment, in the shared `latere` namespace.** replichai deploys to the same cluster and namespace as the other latere services, applied with `kubectl apply -k deploy/prod/`. There is exactly one deploy target for now; `deploy/staging/` is a prepared stub (namespace `latere-staging`, host patches) so a future staging environment is a small diff, not a new setup. Local development is `docker compose` / `make up` and never touches the cluster.

**The `.env` is the config secret; the manifest owns what must not drift.** Deployment secrets — the managed-agents credential, the forge token, the OIDC client, the cookie key, the admin-email bootstrap list — live in a `replichai-config` Kubernetes secret created from the deployment's `.env` (`kubectl create secret --from-env-file`), read via `envFrom`. But `DATABASE_URL`, `ADDR`, `BASE_URL`, and the runtime selector are set as explicit manifest env (the selector is `REPLICHAI_RUNTIME`, which the prod overlay sets to `anthropic-managed`; this spec was written when it was called `PROVIDER`), which override any same-named `.env` key. `DATABASE_URL` in particular comes from the terraform-owned `replichai-db` secret, so the app and the cluster agree on exactly one connection string and neither the `.env` nor a copy can drift from it.

**Reproductions are read from the forge org in production.** The composition root reads the reproduction registry (003) from GitHub when `REPLICHAI_GITHUB_TOKEN` is set (`REPLICHAI_GITHUB_OWNER`, default `open-hai`), and falls back to a filesystem dir only for a local run with no token. So the deployed Published Reproductions list is the published repositories themselves, derived at read time, with no second store to backfill or drift — the same "derived, not stored" rule 003 sets, applied at the deployment seam.

**Fail-closed is the load-bearing production decision.** `DEV` is deliberately unset in the manifest. With no OIDC login wired yet (006c's remaining piece), the gate is `DenyAll`, so production admits *nobody* — which is the safe direction. Setting `DEV` to make it "work" would admit every request as a synthetic principal on a public domain, so the manifest forbids it and the release smoke *asserts* it: a `GET /reproductions` that returns anything but `401/403` fails the release. The dev bypass is loopback-local only, never a production posture.

**Migrations run at startup, so a deploy is the whole install.** The server applies the job and access schemas on boot (`job.Migrate`, `access.Migrate`), each in its own version namespace on the shared database. There is no separate migration job or step — `docker compose up` locally and a pod roll in production both migrate before serving, which is what lets the release pipeline be a plain image roll.

**The ingress is tuned for streaming and long runs.** SSE needs the proxy to not buffer and not time out: the ingress disables response buffering and sets a one-hour read/send timeout, so `/jobs/{id}/events` streams live rather than arriving in one lump at the end. The run itself is not bounded by that hour — a session outlives its watchers by design (006b), and the browser's `EventSource` reconnects when the proxy drops an idle stream, so the viewer picks the run back up where it is.

**A modest connection budget.** The shared cluster has ~25 Postgres slots across every service, so the `replichai-db` DSN pins `pool_max_conns=5` (pgx honours it), keeping replichai a good citizen without a per-service pool cluster.

**Terraform provisions the domain, the database, and the DB secret.** In [`../terraform`](../../terraform): `dns.tf` adds the `replichai.latere.ai` A record to the ingress load balancer; `database.tf` adds the `replichai` database (with `prevent_destroy`, like every other service) and the `replichai-db` Kubernetes secret carrying `DATABASE_URL`. Applied via the existing `tofu` workflow.

**Release is a tag, deploy is the central pipeline.** Three GitHub Actions workflows, matching the latere convention:

- `ci.yml` — vet, `go test -race` against a live Postgres service (so the job and access store contracts run their real `SELECT … FOR UPDATE SKIP LOCKED` path instead of skipping), and build. On every push and PR to `main`.
- `docker.yml` — build and push a ghcr image on every `main` push, for continuous delivery between releases.
- `release.yml` — a `v*` tag calls the central reusable pipeline (`latere-ai/ci/service-release.yml@v1`), which builds the image, applies `deploy/prod/`, rolls the Deployment in `latere`, runs the live smoke, and publishes the GitHub release. **Deploy always targets prod;** there is no CI staging deploy.

`build_mode: dockerfile` — replichai builds everything in one Dockerfile and migrates at startup, so no split build or migrate step is needed.

## Acceptance criteria

1. `docker compose up` brings up Postgres + the server locally, and a submitted paper walks `submitted → done`; verified, and reproducible with `make smoke`.
2. The prod overlay builds (`kubectl kustomize deploy/prod`) into the `latere` namespace with host `replichai.latere.ai`, the `replichai-config` and `replichai-db` secrets, and `REPLICHAI_RUNTIME=anthropic-managed`.
3. Production is fail-closed: the manifest sets no `DEV`, and the release smoke fails if `/reproductions` is not refused — so a dev-bypass build cannot ship to prod unnoticed.
4. One connection string: the manifest `DATABASE_URL` (from the terraform secret) overrides any `.env` value, so the app and cluster cannot disagree.
5. Migrations apply at pod startup; a job survives a restart (Postgres durability) — verified live.
6. A `v*` tag builds, deploys to prod, and publishes a release only if the live smoke passes; the smoke proves liveness (`/healthz`) and the fail-closed gate.
7. Terraform is applied and the domain resolves: `replichai.latere.ai` points at the ingress load balancer, and the `replichai` database and `replichai-db` secret exist.
8. CI runs the Postgres store contracts against a real database, so `SKIP LOCKED` disjointness is asserted on every push, not only locally.

## Out of scope

The OIDC login itself (006c) — until it lands, production is reachable but admits nobody, which is the point. A CI staging deploy (the overlay is a stub). Autoscaling, multi-replica HA, and blue-green — a low-volume single-tenant beta does not need them, and the job store's `SKIP LOCKED` already makes a surge pod safe. A frontend build in the pipeline (there is no frontend yet — 006e). Per-user backend credentials and multi-tenancy, deferred with the rest of 006's single-tenant scoping. Secret rotation and disaster recovery beyond what the shared cluster already provides.

# Deploying replichai

Kustomize manifests for the `latere` Kubernetes cluster. Production is applied
with `kubectl`; staging is a prepared stub (see `staging/`).

## Layout

- `base/` — environment-neutral Deployment, Service, Ingress.
- `prod/` — the production overlay (namespace `latere`, host `replichai.latere.ai`).
- `staging/` — a stub overlay for a future `latere-staging` environment.

## One-time prerequisites (terraform owns these)

The domain, database, and DB secret are provisioned in [`../../terraform`](../../terraform):

- `dns.tf` — the `replichai.latere.ai` A record → the ingress load balancer.
- `database.tf` — the `replichai` database on the shared cluster, and the
  `replichai-db` Kubernetes secret (`DATABASE_URL`) in the `latere` namespace.

Apply them with the terraform workflow (`tofu apply`) before the first deploy.

## The config secret (from `.env`)

The deployment reads its secrets from a `replichai-config` secret via `envFrom`.
Create it from the deployment's `.env` (never commit the `.env`; never read it
into anything but the cluster):

```bash
kubectl -n latere create secret generic replichai-config \
  --from-env-file=.env \
  --dry-run=client -o yaml | kubectl apply -f -
```

`.env` should carry the secret/config values the app needs, for example:

```
REPLICHAI_API_KEY=...          # the managed-agents backend credential
REPLICHAI_GITHUB_TOKEN=...     # the forge token for publishing (006f)
AUTH_ISSUER=https://auth.latere.ai
AUTH_CLIENT_ID=replichai
AUTH_REDIRECT_URL=https://replichai.latere.ai/callback
AUTH_COOKIE_KEY=...            # openssl rand -hex 32 (public OIDC client)
ADMIN_EMAILS=you@latere.ai     # bootstrap platform admins, comma-separated
MAILGUN_API_KEY=...            # outbound mail (006p); omit and invitations are logged, not sent
```

`DATABASE_URL`, `ADDR`, `BASE_URL`, and `PROVIDER` are set by the manifest and
override any same-named key in `.env`, so the two cannot drift.

### Who may sign in

Since [006q](../specs/006q-sign-in-is-open-budget-is-not.md), anyone who
completes the OIDC login is granted `user` — the deployment no longer keeps a
guest list. What limits them is **budget**: the platform's default session quota
is `0`, so a new account can sign in, create a project, and read what it may,
and cannot run a replication session until an admin tops it up or a project
owner sponsors sessions.

`ADMIN_EMAILS` is how a platform admin exists before there is anyone to promote
them: it seeds the grant before their first login, and a self-grant never
promotes or demotes an existing one, so a seeded admin stays one.

Revoking is a **ban**, not an un-invite: the grant row is kept and marked
revoked, so the next sign-in cannot silently re-create it, and the account stays
visible in the admin console to be re-granted. `ADMIN_EMAILS` is replayed
through the bootstrap seeder on every start and deliberately skips a revoked
address, so a restart does not undo a ban either.

The quota migration lowers the platform default to `0` on the deployment that
already holds a higher one, so no manual step follows the deploy. An admin who
wants a different number sets it in **Admin → Platform** afterwards; the
migration is version-gated and never runs again.

### Outbound mail

A project invitation is the one message the platform sends to somebody who is
not looking at it (006p), and it needs a transport:

| variable | where it lives | notes |
|---|---|---|
| `MAILGUN_API_KEY` | `.env` → `replichai-config` secret | a credential; the literal `test` and an empty value both select the log transport |
| `MAILGUN_DOMAIN` | the manifest | policy, not a secret; **required** when a key is set — a key with no domain fails at startup, deliberately |
| `MAILGUN_REGION` | the manifest | `eu` selects the EU API host; anything else selects the US one |
| `SMTP_HOST`/`SMTP_PORT`/`SMTP_USERNAME`/`SMTP_PASSWORD`/`SMTP_FROM` | `.env` | used only when no Mailgun key is set; port 587 (STARTTLS) is the default and 465 selects implicit TLS |

With none of them set the server logs each message and starts normally — a
deployment never fails to boot for want of mail, and an invitation is never
silently dropped without a trace. `BASE_URL` is what every link in a message is
built from, so a wrong value sends people to the wrong host.

## Build and push the image

There is no release pipeline yet; build and push by hand (a `Dockerfile` is at
the repo root):

```bash
podman build -t ghcr.io/latere-ai/replichai:$(git rev-parse --short HEAD) .
podman push ghcr.io/latere-ai/replichai:$(git rev-parse --short HEAD)
```

## Deploy

```bash
kubectl apply -k deploy/prod/
kubectl -n latere set image deploy/replichai replichai=ghcr.io/latere-ai/replichai:<tag>
kubectl -n latere rollout status deploy/replichai
```

## Access note

The gate is **fail-closed**: with no OIDC login wired yet (006c's login front
door is the remaining piece), production admits nobody. `DEV` is deliberately
unset — enabling it would admit every request as a synthetic principal on a
public domain. Land the login before expecting anyone to sign in.

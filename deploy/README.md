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
```

`DATABASE_URL`, `ADDR`, `BASE_URL`, and `PROVIDER` are set by the manifest and
override any same-named key in `.env`, so the two cannot drift.

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

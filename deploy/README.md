# Deploying replichai

Kustomize manifests for the `latere` Kubernetes cluster. Production is deployed
by the release pipeline on every `v*` tag (see [Release](#release)); staging is
a prepared stub (see `staging/`).

## Layout

- `base/`: what every environment shares: the Deployment with the settings
  that are the same everywhere, the Service, the Ingress class and its
  annotations, the PodDisruptionBudget. Its image tag is the placeholder
  `unreleased`, which is never pushed.
- `prod/`: namespace `latere`, host `replichai.latere.ai`, the OIDC client
  registered for it, the bootstrap admin list, and the release it runs, pinned
  with an `images` entry.
- `staging/`: a stub for a future `latere-staging` environment, with its own
  placeholders for the same values.

## One-time prerequisites (terraform owns these)

The domain, database, and DB secret are provisioned by terraform in the latere
infrastructure repository, which is not part of this one:

- `dns.tf`: the `replichai.latere.ai` A record → the ingress load balancer.
- `database.tf`: the `replichai` database on the shared cluster, and the
  `replichai-db` Kubernetes secret (`DATABASE_URL`) in the `latere` namespace.

Apply them there (`tofu apply`) before the first deploy.

## The config secret (from `.env`)

The deployment reads its credentials from a `replichai-config` secret via
`envFrom`. Create it from the deployment's `.env`. Never commit the `.env`, and
never read it into anything but the cluster:

```bash
kubectl -n latere create secret generic replichai-config \
  --from-env-file=.env \
  --dry-run=client -o yaml | kubectl apply -f -
```

`.env` carries credentials and nothing else:

```
ANTHROPIC_API_KEY=...          # the anthropic-managed runtime credential
REPLICHAI_GITHUB_APP_ID=...    # the GitHub App the server publishes as (009), and
REPLICHAI_GITHUB_APP_KEY=...   # its private key: the PEM text, or a path to it.
                               # REPLICHAI_GITHUB_TOKEN is the PAT fallback when neither is set
AUTH_COOKIE_KEY=...            # openssl rand -hex 32
AUTH_CLIENT_SECRET=            # only for a confidential OIDC client; prod's is public PKCE, so empty
MAILGUN_API_KEY=...            # outbound mail (006p); omit and invitations are logged, not sent
DRAFT_API_KEY=...              # only for drafting on a *non*-Anthropic endpoint (039); Anthropic reuses ANTHROPIC_API_KEY
STRIPE_SECRET_KEY=...          # card payments (022b); omit and checkout answers payments_unconfigured
STRIPE_WEBHOOK_SECRET=...
```

Everything else is set by the manifests. In Kubernetes a container's own `env`
entry takes precedence over the same key arriving through `envFrom`, so a
policy value put in the secret is not an override; it is ignored. The base
sets what every environment shares: `DATABASE_URL` (from the terraform-owned
`replichai-db` secret), `ADDR`, `REPLICHAI_RUNTIME`, `AUTH_ISSUER`,
`MAILGUN_DOMAIN`, `MAILGUN_REGION`. Each overlay sets what is its own:
`BASE_URL`, `AUTH_CLIENT_ID`, `AUTH_REDIRECT_URL`, `ADMIN_EMAILS`. Change those
in the overlay, where the change is reviewed, not in `.env`.

### Which model drafts a description

[039](../specs/039-a-description-can-be-drafted.md) lets a project's owner ask a
model for a first draft of its description.

**On Anthropic, it is one field.** The deployment already holds an Anthropic
credential for the agent that runs its sessions, so drafting reuses it: pick
Anthropic in the admin console's policy card, type a model id, save. Nothing
else needs setting, and no second key is needed.

**On another vendor**, drafting needs what the platform does not already have:
an endpoint (anything speaking the OpenAI chat-completions protocol: a hosted
vendor, or your own llama.cpp / vLLM / Ollama) and a key of its own in
`DRAFT_API_KEY`. The key stays in this secret and never goes in the database: a
settings row is read by every admin, lands in every backup, and would be
printed by any debug route somebody adds later.

Either way the model id must be priced by the rate card on the same screen: an
exact entry, or a dated snapshot of one (`claude-haiku-4-5-20251001` prices as
`claude-haiku-4-5`). Without one the deployment refuses to draft rather than
guessing. The card's "charge the dearest rates" fallback exists for an
unrecognized *session* model, and applying it here would bill Opus rates for a
model that costs a fraction of that.

With no model configured, nothing breaks: the app offers no draft control and
descriptions are typed by hand.

### Who may sign in

Since [006q](../specs/006q-sign-in-is-open-budget-is-not.md), anyone who
completes the OIDC login is granted `user`; the deployment no longer keeps a
guest list. What limits them is **money** ([011](../specs/011-what-a-session-costs.md)):
a new account arrives with no credits, so it can sign in and read what it may,
and cannot run a replication session until an admin adds credits to its wallet
or it is invited into a project somebody else has funded.

`ADMIN_EMAILS` is how a platform admin exists before there is anyone to promote
them: it seeds the grant before their first login, and a self-grant never
promotes or demotes an existing grant, so a seeded admin stays one.

Revoking is a **ban**, not an un-invite: the grant row is kept and marked
revoked, so the next sign-in cannot silently re-create it, and the account stays
visible in the admin console to be re-granted. `ADMIN_EMAILS` is replayed
through the bootstrap seeder on every start and deliberately skips a revoked
address, so a restart does not undo a ban either.

**The server checks its own price list at every boot**, and the pod log says what it found:

```
{"level":"WARN","msg":"rates: the card does not name this model, so it is charged the dearest rates on the card; add it in Admin, Platform to bill it at its real price","preference":"best","model":"claude-opus-5-20260901","input_per_mtok":5,"output_per_mtok":25}
{"level":"INFO","msg":"rates: preference priced at its own rates","preference":"balanced","model":"claude-sonnet-5"}
```

Read that check after a deploy. It runs in the server because the server is the
one process holding both this deployment's rate card and the provider
credential, so nothing has to be assembled by hand. It logs and never blocks: a
card one release behind is a billing imprecision, not a reason to refuse to
boot. A preference resolving to an id the card does not name is still charged,
at the dearest rates on the card, which is never free but is not that model's
own price either.

`replichai rates` runs the same comparison from a laptop (`-json` for a pipeline). The production image ships only the server, so that command is for a local check against `DATABASE_URL`, not something to exec in the cluster.

Credits are the meter. A session charges the **project** it belongs to, out of
credits somebody allocated to that project from their own wallet; a project must
hold the **start reserve** before a session may begin there, and that reserve is
held for the run and returned when it settles. The reserve is seeded at a
deliberately non-zero default and set in **Admin → Platform**; credits are added
per person in **Admin → People**. The migration drops the old session counter
and its top-up table outright: a count and a balance are different quantities,
so nothing is carried across and everyone starts at zero.

### Outbound mail

A project invitation is the one message the platform sends to somebody who is
not using the app (006p), and it needs a transport:

| variable | where it lives | notes |
|---|---|---|
| `MAILGUN_API_KEY` | `.env` → `replichai-config` secret | a credential; the literal `test` and an empty value both select the log transport |
| `MAILGUN_DOMAIN` | the manifest | policy, not a secret; **required** when a key is set. A key with no domain fails at startup, deliberately |
| `MAILGUN_REGION` | the manifest | `eu` selects the EU API host; anything else selects the US one |
| `SMTP_HOST`/`SMTP_PORT`/`SMTP_USERNAME`/`SMTP_PASSWORD`/`SMTP_FROM` | `.env` | used only when no Mailgun key is set; port 587 (STARTTLS) is the default and 465 selects implicit TLS |

With none of them set the server logs each message and starts normally: a
deployment never fails to boot for want of mail, and an invitation is never
dropped without a trace. `BASE_URL` is what every link in a message is built
from, so a wrong value sends people to the wrong host.

## Release

A release is a tag, and the pipeline does the rest. Write the release's notes
under `Unreleased` in `CHANGELOG.md`, then cut it:

```bash
go tool lateregate release v0.31.0
```

It refuses while CI is red; otherwise it moves the notes into a
section for the tag, rewrites the `newTag` in `deploy/prod/kustomization.yaml`
to the tag, commits, tags, and pushes.

`.github/workflows/release.yml` hands the tag to the shared `service-release`
pipeline (`latere-ai/ci`), which builds the image from the root `Dockerfile`,
pushes it to `ghcr.io/latere-ai/replichai`, applies `deploy/prod/` in the
`latere` namespace, rolls the Deployment onto the new image, runs
`test/release-smoke.sh` against `https://replichai.latere.ai`, and publishes
the GitHub release with the tag's section of `CHANGELOG.md` (the pre-push hook
refuses a release tag that has none). Between releases, `docker.yml` builds an
image from every push to `main`, tagged by branch and by sha, so a commit can
be tried in a cluster without cutting a release.

Nothing deploys staging: the overlay is a prepared stub.

### By hand, when the pipeline is down

The pipeline does nothing a laptop with cluster access cannot do:

```bash
podman build -t ghcr.io/latere-ai/replichai:$(git rev-parse --short HEAD) .
podman push ghcr.io/latere-ai/replichai:$(git rev-parse --short HEAD)
kubectl apply -k deploy/prod/
kubectl -n latere set image deploy/replichai replichai=ghcr.io/latere-ai/replichai:$(git rev-parse --short HEAD)
kubectl -n latere rollout status deploy/replichai
test/release-smoke.sh
```

`deploy/prod/` pins the last release, so the `apply` alone runs that release;
the `set image` is what moves production to a build that is not a release. The
next release's apply moves it back onto the pinned tag. Nothing in `deploy/`
names `:latest`: the base's placeholder never pulls, so an overlay that pins no
release fails visibly instead of running whatever was published last.

## `DEV` stays unset

The gate is **fail-closed**, and the one way to open it by accident is `DEV`.
It exists so a developer can work without an identity provider, and it admits
every request as a synthetic principal. On a public domain that is not a
relaxed gate; it is no gate. It is therefore never set in an overlay, and
`test/release-smoke.sh` drives `/projects` against the deployed base URL after
every tagged release and fails unless the request is refused, so the bypass
reaching production is a failed release rather than a silent hole.

Who may sign in once the gate is doing its job is described above.

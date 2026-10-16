# Running your own deployment

The replichai web application is one Go program, `cmd/server`: the HTTP API,
the built frontend, and the worker that runs audits share a process, and
Postgres holds everything that has to outlive it. This page takes you from a
clone to a deployment of your own. The hosted service at `replichai.latere.ai`
runs the same code, and [`deploy/`](../deploy/README.md) shows how it is
deployed there, as one worked example.

## What you need

| | Required | What for |
|---|---|---|
| Postgres | yes | every store. The compose file and CI run version 17. Migrations run at startup |
| an OpenID Connect issuer | yes | signing people in. Any provider that implements OpenID Connect discovery; see [Choosing an identity provider](#choosing-an-identity-provider) |
| a runtime credential | depends on the runtime | `anthropic-managed` runs sessions on Anthropic's Managed Agents backend and needs an Anthropic API key. `local` runs them on the server's own machine through a coding agent installed and logged in there, and needs no key in replichai's configuration. `memory` produces fake audits and needs nothing |
| a GitHub App, or a personal access token | no | publishing each reproduction to its own repository in a GitHub organization you control. Without either, audits still run and keep only their transcript |
| Stripe | no | card payments. Without it, checkout is refused and credits come only from an admin |
| Mailgun, or an SMTP server | no | sending project invitations. Without either, each message is written to the log instead |

## Configuration

[`example.env`](../example.env) is the one list of every environment variable
the server and the CLI read, grouped by purpose, each with its default and what
it is for. Copy it to `.env` and fill it in; this page does not repeat the
values. A test in `internal/doccheck` fails when the code reads a variable that
`example.env` does not name, or `example.env` names one the code no longer
reads, so the file stays in step with the code.

The groups, and what to decide in each:

- **The server.** `BASE_URL` is the public address; every link in an outbound
  message and the default sign-in redirect are built from it. `DATABASE_URL` is
  the direct Postgres endpoint, where migrations run; `DATABASE_POOL_URL` is
  optional, for a transaction-mode pooler in front of the same database. With
  no `DATABASE_URL` the server keeps everything in memory and loses it on
  restart, which is for development only. `REPLICHAI_RUNTIME` picks the
  runtime a session runs on when it names none; `REPLICHAI_RUNTIMES` offers
  others beside it, so a person chooses when they start a session.
- **Runtime credentials.** `ANTHROPIC_API_KEY` for `anthropic-managed`. Each
  runtime reads only its own variables. Offering `latere` (the Latere
  platform, where a session runs as a hosted agent session) needs no platform
  key of the server's own: each session acts for the person who started it
  and is paid from their own Latere wallet, and the project's credits are not
  touched. To act for them while the session runs, the server asks the issuer
  for `offline_access` and keeps each person's sign-in sealed with
  `REPLICHAI_SIGNIN_KEY`, without which the server refuses to start. It
  refreshes the sign-in as its browser client, public or confidential. This
  works only with Latere's own issuer, whose client for the deployment must
  allow `offline_access` and a token for the platform's API.
- **Signing in through the browser.** The issuer, the client registered with
  it, and the key that signs the session cookie. See the next section.
- **The terminal talking to this deployment.** Read by the CLI on users'
  machines, not by the server. See
  [The terminal's sign-in](#the-terminals-sign-in).
- **GitHub.** The App or token reproductions are published with, and the
  organization they are created in.
- **Card payments**, **drafting a project description**, **outbound mail**:
  optional, as above. When the runtime is `anthropic-managed` and an admin
  picks an Anthropic model for drafting, drafting reuses `ANTHROPIC_API_KEY`;
  any other endpoint needs `DRAFT_API_KEY`.
- **Running sessions on this machine.** The local runtime's settings, read
  when `REPLICHAI_RUNTIME=local`.
- **Tests.** For a developer's shell, never a deployment.

Several defaults in `example.env` are the hosted service's own values: the
issuer and audience, the GitHub organization, and the mail sender. A deployment
of your own changes each of them.

The server also takes command-line flags that mirror some of these variables;
run it with `-h` to list them. One has no variable: `-workers`, the number of
job workers, and so of audits, one process runs at once.

Traces and logs are exported over OTLP when the standard
`OTEL_EXPORTER_OTLP_ENDPOINT` (and, if your collector needs it,
`OTEL_EXPORTER_OTLP_HEADERS`) is set. With neither, logs go to standard output
only.

## Choosing an identity provider

replichai has no accounts of its own. It signs people in through an OpenID
Connect issuer, takes their subject and email from it, and decides what they
may do from its own access table, never from claims in the token.

Register a client with your provider for this deployment:

- The redirect URI is `$BASE_URL/callback`, unless you set
  `AUTH_REDIRECT_URL`.
- A public client using PKCE needs no secret. A confidential client puts its
  secret in `AUTH_CLIENT_SECRET`.
- The server requests the scopes `openid`, `email` and `profile`, and needs
  the email: from the ID token, or from the provider's userinfo endpoint.
  When it offers the `latere` runtime it also requests `offline_access`.

Then set, together, `AUTH_ISSUER`, `AUTH_CLIENT_ID`, `AUTH_AUDIENCE`,
`AUTH_COOKIE_KEY` (`openssl rand -hex 32`) and `BASE_URL`. They are one unit:

- The browser sign-in reads `AUTH_ISSUER` and falls back to `auth.latere.ai`
  when it is unset, while the bearer path the CLI uses reads it with no
  fallback and is switched off without it. Leaving it unset therefore points
  the browser sign-in at the hosted service's issuer. Set it.
- With `AUTH_ISSUER` set, `AUTH_AUDIENCE` is required, and the server refuses
  to start without it. It is the one audience a bearer token must carry.
- The server refuses to start without a valid `AUTH_COOKIE_KEY`.

`AUTH_PROVIDER` selects how the claims of your provider's tokens are read. It
accepts `latere` (the default), `keycloak`, `google` and `cognito`; those are
the issuer kinds the `authkit/oidc` package in `latere.ai/x/pkg` maps. The
kinds differ in where a provider puts names and group membership; subject and
email are read the same way. The browser sign-in finds the provider's endpoints
through discovery (`$AUTH_ISSUER/.well-known/openid-configuration`) and ends
the provider's session on sign-out where the provider advertises an
end-session endpoint.

The bearer path does not use discovery. It fetches the issuer's signing keys
from `AUTH_JWKS_URL`, which defaults to `$AUTH_ISSUER/.well-known/jwks.json`.
If your provider publishes its keys elsewhere (the `jwks_uri` in its discovery
document says where), set `AUTH_JWKS_URL` to that address.

### The terminal's sign-in

The CLI's commands that talk to the app (`login`, `org`, `project`, `push`, and
`audit --publish --project`) sign in differently from the browser, and expect
more of the provider. Users point them at your deployment with
`REPLICHAI_AUTH_URL` and `REPLICHAI_API_URL` (see
[`cli.md`](cli.md#using-your-own-deployment)). Then:

- The CLI does not use OpenID Connect discovery. It calls `/device/code` and
  `/token` directly under `REPLICHAI_AUTH_URL`, as a public client with the
  fixed id `replichai-cli` using the device authorization grant.
- Before each call to the app it asks `/actor-tokens` under the same URL for a
  short-lived token for the audience `replichai.latere.ai`, a fixed value. If
  that endpoint is missing or refuses, it presents the stored access token
  instead.
- The server accepts a bearer token only when it was issued by `AUTH_ISSUER`,
  is signed by a key at `AUTH_JWKS_URL`, and carries `AUTH_AUDIENCE`. Whichever
  token the CLI presents has to satisfy all three.
- `org` switches context by sending `organization_id` on a refresh-token grant,
  which only an identity service that supports organizations understands.

A provider laid out differently still serves the browser sign-in, which
discovers its endpoints, while those CLI commands cannot sign in against it.
The CLI's other commands need no app and are unaffected.

## Who becomes an admin

Anyone who completes the sign-in is granted the ordinary user tier. An account
starts with no credits, so it can sign in and read but cannot start a session
until it has some.

`ADMIN_EMAILS` is a comma-separated list of addresses seeded as platform admins
at every start. It is how the first admin exists before there is anyone to
promote them. The address has to be the email your identity provider reports
for that person. Revoking someone's access in the app is recorded rather than
deleted, and a revoked address in `ADMIN_EMAILS` is skipped at the next start,
so a restart does not undo it.

An admin adds credits to a person's wallet in **Admin → People**, and sets the
start reserve (what a project must hold before a session may start) and the
rate card (the per-model prices sessions are charged at) in
**Admin → Platform**. The server
logs at every start whether the rate card names the models the runtime
resolves to; `replichai rates` runs the same check from a terminal (see
[`cli.md`](cli.md#replichai-rates-flags)).

You do not type Anthropic's prices into the rate card. At every start and then
every hour, the server reads OpenRouter's public model list, which states
Anthropic's list prices, and writes the price of every Anthropic model into the
card, adding the models the card lacks. A row for a model the list prices
follows the list: **Admin → Platform** shows those rows under the list's name,
with the time the server last applied it, and does not let you edit or remove
them, and a save keeps them at the list's prices. A row for any other model
stays as you set it. A model priced by prompt length, such as
Claude Haiku 5.5, is charged at its lower tier, because a session's usage is a
total over many requests. Set `REPLICHAI_PRICE_LIST_URL` to read another list
in the same format, or to `off` to price every model by hand. If the list
cannot be read, the card keeps the prices it has.

Card payments add a second way in. Set `STRIPE_SECRET_KEY`, create a webhook
endpoint in Stripe at `$BASE_URL/stripe/webhook`, and put its signing secret in
`STRIPE_WEBHOOK_SECRET`. The webhook is the only path that credits a payment.
The Stripe adapter reads `checkout.session.completed`,
`checkout.session.async_payment_succeeded`, `payment_intent.succeeded`,
`payment_intent.payment_failed`, `charge.refunded` and
`charge.dispute.created`.

## The dev bypass is for your own machine

`DEV=true` skips sign-in entirely and admits every request as one synthetic
principal, `dev@local`. It exists so a developer can run the app without an
identity provider. On a reachable host it is not a weaker gate; it is no gate.

The server refuses `DEV` unless `ADDR` binds a loopback address (`127.0.0.1`,
`::1` or `localhost`). `DEV_INSECURE=true` lifts that check for a container,
whose server has to bind all interfaces; the compose file sets both. Never set
either on a deployment others can reach.

## Running with docker compose, to evaluate

```bash
cp example.env .env      # optional: compose substitutes values from it
make up                  # docker compose up --build; Podman works too
```

`make up` builds the image and starts it with Postgres. Open
`http://localhost:8080`. It runs with the dev bypass, so you are signed in as
`dev@local`, which the compose file seeds as an admin; there is no OpenID
Connect sign-in. It uses the `memory` runtime unless `.env` sets
`REPLICHAI_RUNTIME=anthropic-managed` and `ANTHROPIC_API_KEY`. The compose file
passes no GitHub settings, so audits keep only their transcript. Invitations
are written to `docker compose logs server`. `make down` stops it and deletes
the database volume.

Compose publishes port 8080 on all of the host's interfaces, and the dev bypass
admits anyone who reaches it. Run it only where that port is not reachable from
a network you do not trust.

## Building and running the container image

The [`Dockerfile`](../Dockerfile) builds the frontend with bun, compiles the
server with the frontend embedded, and ships the binary on an empty (`scratch`)
base with the `method/` directory and CA certificates. It runs as a non-root
user, listens on 8080, and has no `HEALTHCHECK`; point your orchestrator's
probes at `GET /healthz`.

```bash
docker build -t replichai --build-arg VERSION="$(git describe --tags --always)" .
docker run --rm -p 8080:8080 --env-file .env replichai
```

`VERSION` is the version the server reports in telemetry; it is optional.
`--env-file` reads the same `.env`, whose comments sit on their own lines so
the file works there unchanged.

The server speaks plain HTTP. Put a proxy or load balancer that terminates TLS
in front of it, and give `BASE_URL` the `https://` address: the session cookie
is marked `Secure` and given the `__Host-` prefix only when `BASE_URL` starts
with `https`.

Several replicas can share one database. A session becomes a row before
anything runs, and a worker claims it with `SELECT … FOR UPDATE SKIP LOCKED`, so
two processes never run the same audit.

**The image cannot run the `local` runtime.** It contains the server, the
method and CA certificates, and no shell, coding agent or sandbox. To use
`REPLICHAI_RUNTIME=local`, run the server binary directly on a machine prepared
as [`local-runs.md`](local-runs.md) describes:

```bash
(cd frontend && bun install && bun run build)   # without this, a placeholder page is embedded instead of the app
go build -o replichai-server ./cmd/server
./replichai-server                              # from the clone, so ./method is found; or set METHOD_DIR
```

## Publishing with a GitHub App

Each reproduction gets a private repository in the organization named by
`REPLICHAI_GITHUB_OWNER`. Its default is the organization the hosted service
publishes into, so set your own. It has to be an organization, not a personal
account, because repositories are created through GitHub's organization
endpoint.

A GitHub App is the preferred credential. Install it on that organization with
read and write access to repository administration and contents: the server
creates repositories, writes files into them, invites collaborators and changes
visibility. Set `REPLICHAI_GITHUB_APP_ID` and `REPLICHAI_GITHUB_APP_KEY` (the
PEM text, or a path to the `.pem` file). With an App, the server's own GitHub
calls run as the App's installation, and the app can give
`replichai audit --publish --project` a token limited to one repository that
expires within the hour. A personal access token cannot mint such a token. An
App that is configured but invalid stops the server at startup rather than
falling back.

`REPLICHAI_GITHUB_TOKEN` is a personal access token that can create and push
repositories in the organization. It is the fallback for the server's own calls
when no App is configured, and it is also the credential a running session's
repository mount authenticates with. It is handed to the runtime, so on the
hosted runtime the sandbox holds it for the length of the run. [`architecture.md`](architecture.md#trust-boundaries)
explains why that one credential is not yet short-lived.

With a credential configured, the server also reads the organization's
published reproductions as its registry, which is how it warns about a paper
already reproduced and keeps repository names unique. Without one, it reads
`REPRO_DIR`, a directory on disk, instead.

## The Kubernetes manifests in `deploy/`

[`deploy/`](../deploy/README.md) holds the Kustomize manifests Latere uses to
run `replichai.latere.ai`: a base Deployment, Service, Ingress and
PodDisruptionBudget, a production overlay with that host's values, and the
identity its release pipeline deploys with. They are a worked example of one
way to run the image, not something a deployment needs. To adapt them, change
the overlay's host, OIDC client and admin list, the base's issuer, audience and
mail domain, the image name, and the namespace, and supply the
`replichai-config` and `replichai-db` Secrets the Deployment reads.

# The `replichai` command line

`replichai` audits a paper from a terminal: one paper, or a project file of
many. It needs no account and no database for that. A few commands talk to a
replichai web application, either the hosted one at `replichai.latere.ai` or a
deployment of your own; they are grouped together below.

Build it from a clone with Go 1.27 or newer:

```bash
go build -o replichai ./cmd/replichai
./replichai help
```

`replichai help` lists every command, and `replichai <command> -h` prints the
flags that command takes with their defaults. That output is generated from the
flag definitions, so when this page and it disagree, the `-h` output is right.
Flags may come before or after the positional arguments, and `-flag` and
`--flag` are the same.

A **paper reference** (`<paper-ref>` below) is a DOI, an arXiv URL or bare ID,
a publisher URL, or a PDF URL.

Commands that run or publish an audit read the method (the agent's standing
instructions, the rubric and the task template) from a `method/` directory.
With no `--method` they search upward from the working directory for one, so
they work from anywhere inside a clone; from elsewhere, pass
`--method <path-to-clone>/method`.

## Runtimes

A **runtime** is where the agent session runs. Four ship:

| Runtime | Where the session runs | What it needs |
|---|---|---|
| `memory` | in the process, with no model behind it; it produces a fake audit | nothing. Use it to try a command without spending anything |
| `anthropic-managed` | on Anthropic's Managed Agents backend | `ANTHROPIC_API_KEY`, and `ANTHROPIC_BASE_URL` for a non-default endpoint |
| `latere` | on the Latere platform, as a hosted agent session, on any model the platform serves you; your own Latere wallet pays for it | your sign-in from `replichai login` ([Signing in and the app](#signing-in-and-the-app)): the session runs as the account `replichai whoami` shows, in its organization context. To run as another credential, set `LATERE_API_KEY` to a personal access token from the Latere console (Keys, for Agents), or `LATERE_TOKEN_COMMAND` to a command you supply that prints a bearer addressed to `api.latere.ai`; either one, when set, is used instead of the sign-in. `latere print-token` is not such a command: it prints the Latere CLI's login token, which only Latere Auth accepts, so the platform answers 401. Your Latere network preference must be open for an audit |
| `local` | on this machine: a coding agent (Claude Code or Codex) inside a sandbox | the agent installed and logged in, and the sandbox. [`local-runs.md`](local-runs.md) is the guide |

The model is chosen with `--model`, as an **Effort** level or as one exact model:
`quick`, `thorough` or `sustained` (the older names `fast`, `balanced` and
`best` still work), or `provider/id`. On Anthropic each level is one Claude
model. On Latere each level is the platform's own level of that name, whose
models the platform chooses, and `provider/id` can be any model it serves you.
`replichai models --runtime <name>` lists both.

### Environments

The environment is what the session's machine runs: an image with the tools a
kind of work needs already installed. `--environment`, or `environment:` in a
project file for the whole file or for one paper, names it, and the run record
in `replication.json` says which one a run had. A runtime that does not offer
the named environment refuses the run before it starts, rather than running it
on another.

On `latere`, the environments are the platform's: `base` (Python with its data
and document libraries), `science` (adds SciPy, statsmodels, scikit-learn,
pingouin and the spatial libraries, and R with lme4, lmerTest, ordinal, car,
emmeans, afex, psych, spdep and the tidyverse) and `gui` (adds a display and a
browser). A run from your own machine may name any of them. In the app, an
admin chooses which ones sessions may name, in Admin, Platform policy, because
one is usable only once the platform holds its image; `base` and `science` are
on to start with.

On `anthropic-managed`, `base` is Anthropic's own image and `science` installs
the same Python packages on it, and R with the same packages from Ubuntu's R
packages. Sessions that ask for the same environment, packages and network
share one Anthropic environment, so Anthropic installs the packages once and
reuses them; the first session on a new combination waits for the install.
The other runtimes offer no named environment yet.

The runtime is chosen by `--runtime`, then by `REPLICHAI_RUNTIME`, then by the
command's own default: `anthropic-managed` for `audit`, `finish` and `rates`,
and `local` for `run`. `replichai providers` lists the installed runtimes and
marks the one a command without `--runtime` would use.

The flags marked "local runtime" in the tables below are read only with
`--runtime local`. Each has an environment variable, so a machine can be
configured once:

| Flag | Variable | Default |
|---|---|---|
| `--sandbox` | `REPLICHAI_SANDBOX` | `host`, which uses srt (Anthropic's sandbox runtime); `container` uses Podman or Docker |
| `--harness` | `REPLICHAI_HARNESS` | `claude-code`; or `codex`. The harness is the coding agent's command-line program that runs inside the sandbox |
| `--engine` | `REPLICHAI_CONTAINER_ENGINE` | with `--sandbox container`: Podman, then Docker |
| `--image` | `REPLICHAI_HARNESS_IMAGE` | with `--sandbox container`: none, and required, since no harness image is published |
| `--models` | `REPLICHAI_MODELS_FILE` | `~/.config/replichai/models.yaml`; with no file, the harness's own login |
| `--session-root` | `REPLICHAI_SESSION_ROOT` | `~/.replichai/sessions` |
| `--retention` | `REPLICHAI_SESSION_RETENTION` | `keep`; or `on-failure`, `discard` |

A flag given on the command line wins over its variable.

## One audit

### `replichai audit <paper-ref> [flags]`

Audits one paper and writes its reproduction into `<out>/<paper>/`, and the
transcript into `<out>/<paper>-transcript.jsonl`, one event per line. With
`--publish` the reproduction also gets its own GitHub repository (see
[Publishing](#publishing)).

The work is kept as the run goes, not only at its end. On the `local` and
`latere` runtimes, every five minutes the files the session has made so far
are written into `<out>/<paper>/` and the transcript so far beside it, so a run
that fails, times out or is stopped still leaves what it had done. On `latere`
the files advance as each of the agent's turns ends.

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `runs` | directory for the artifacts |
| `--runtime` | `$REPLICHAI_RUNTIME`, then `anthropic-managed` | the runtime |
| `--model` | `best` | an Effort level (`quick`, `thorough`, `sustained`, or `fast`, `balanced`, `best`) or one exact model as `provider/id` |
| `--environment` | the runtime's base | the environment the session's machine runs, such as `science`, where the runtime offers it (see [Environments](#environments)) |
| `--max-revisions` | `3` | how many times a graded run may be revised; `0` grades it once and never revises it |
| `--timeout` | `1h0m0s` | the longest the run may take |
| `--interval` | `20s` | time between status checks |
| `--method` | discovered | path to a method directory |
| `--publish` | off | create a GitHub repository and publish the reproduction to it |
| `--owner` | `$REPLICHAI_GITHUB_OWNER`, then `open-hai` | with `--publish`: the GitHub organization the repository is created in |
| `--invite` | none | with `--publish`: a GitHub login to invite as a collaborator |
| `--project` | none | with `--publish`: a project in the app, which creates the repository and hands the run a credential for it |
| `--detach` | off | with `--publish`: start the run, print the `finish` command, and exit |
| `--sandbox`, `--harness`, `--engine`, `--image`, `--models`, `--session-root`, `--retention` | see [Runtimes](#runtimes) | local runtime |

```bash
replichai audit https://arxiv.org/abs/2603.01795 --runtime memory        # a free dry run
replichai audit https://arxiv.org/abs/2603.01795                          # anthropic-managed
replichai audit https://arxiv.org/abs/2603.01795 --runtime local          # this machine
replichai audit https://arxiv.org/abs/2603.01795 --runtime latere --model thorough   # the Latere platform
```

### `replichai resolve <paper-ref> [flags]`

Prints the identity and the repository slug a reference resolves to. Nothing
else runs, so it is a cheap way to check a reference before an audit.

| Flag | Default | Meaning |
|---|---|---|
| `--timeout` | `30s` | network timeout for one fetch |

## A project of papers

### `replichai run <project-file> [flags]`

Audits every paper in a project file, in parallel, one isolated session each,
and keeps `batch.json` (the state) and `report.md` (the results table) current
beside the project file. One paper failing does not stop the rest; the exit
code is non-zero if any failed. The project file format, with per-paper
overrides, repeat runs and released-artifact snapshots, is described in
[`local-runs.md`](local-runs.md#a-whole-project-at-once).

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `runs` | directory for the artifacts |
| `--runtime` | `$REPLICHAI_RUNTIME`, then `local` | the runtime |
| `--parallel` | half the cores, at most 4 | how many papers run at once |
| `--resume` | off | skip papers that already finished and retry the ones that failed |
| `--network` | `artifacts` | network profile for runs whose project file names none: `artifacts` or `open` |
| `--environment` | the runtime's base | environment for runs whose project file names none (see [Environments](#environments)) |
| `--max-revisions` | `3` | per run; `0` grades each run once and never revises it |
| `--timeout` | `1h0m0s` | the longest one paper may take |
| `--interval` | `20s` | time between status checks |
| `--method` | discovered | path to a method directory |
| `--publish` | off | publish each reproduction to its own repository |
| `--owner` | `$REPLICHAI_GITHUB_OWNER`, then the project file's `publish.owner`, then `open-hai` | with `--publish`: the GitHub organization |
| `--invite` | the project file's `publish.invite` | with `--publish`: a GitHub login to invite |
| `--sandbox`, `--harness`, `--engine`, `--image`, `--models`, `--session-root`, `--retention` | see [Runtimes](#runtimes) | local runtime |

`run` takes no `--model`; the project file sets the model, for the whole file
or per paper. It has no `--project` either, so `run --publish` always
publishes with `REPLICHAI_GITHUB_TOKEN`.

## Publishing

With `--publish`, `audit` and `run` create a private GitHub repository for each
reproduction before the session starts, and the agent's commits go into it. When
the run ends, replichai itself, not the agent, verifies what was committed,
writes `replication.json`, commits the transcript, and invites the
collaborator.

**Name your own organization.** The default owner, `open-hai`, is the GitHub
organization the hosted service publishes into, and a token of yours cannot
create repositories there. Set `--owner` or `REPLICHAI_GITHUB_OWNER` to an
organization you control. It has to be an organization rather than a personal
account, because repositories are created through GitHub's organization
endpoint. Then export a token that can create and push repositories in it:

```bash
export REPLICHAI_GITHUB_OWNER=my-lab
export REPLICHAI_GITHUB_TOKEN=...        # never commit this
replichai audit <paper-ref> --publish --invite <github-login>
```

`REPLICHAI_GITHUB_API_URL` points the CLI at another GitHub API root, such as a
GitHub Enterprise server; it is unset for github.com.

On the `latere` runtime, where the agent does not push into the repository
itself, replichai pushes the session's work as it goes: every five minutes,
when something changed, the files so far are committed and pushed, after the
same check the final push makes. The repository shows the work arriving, and a
run that dies keeps everything up to its last push. The final push lands on
top of that history.

`audit --publish --project <id>` is the alternative to holding a token: the app
the project lives in creates the repository and hands the run a credential for
that one repository, valid for at most an hour. It needs `replichai login`
first, and the app's deployment needs a GitHub App configured; without one the
CLI says so and falls back to `REPLICHAI_GITHUB_TOKEN`. That credential can push
but not administer the repository, so `--invite` is refused with `--project`;
add the collaborator in the app.

### `replichai finish <owner>/<repo> <paper-ref> [flags]`

Reattaches to a run started with `audit --publish --detach`, waits for it, and
finalizes the repository: verify the tree, write `replication.json`, commit the
transcript, invite. It needs `REPLICHAI_GITHUB_TOKEN`. Given neither
`--session-ref` nor `--session`, it finalizes the repository from what is
already committed and records the verdict `--verdict` names, or `unknown`.

| Flag | Default | Meaning |
|---|---|---|
| `--session-ref` | none | the session reference `audit --detach` printed; it names its own runtime |
| `--session` | none | a bare session id instead of a reference; the runtime then comes from `--runtime` |
| `--runtime` | `$REPLICHAI_RUNTIME`, then `anthropic-managed` | with `--session`: the runtime the session ran on |
| `--verdict` | none | the verdict to record when no session is given; the same values as `publish --verdict` |
| `--invite` | none | a GitHub login to invite as a collaborator |
| `--method` | discovered | the method directory the audit used |
| `--timeout` | `1h0m0s` | the longest to wait for the session |
| `--interval` | `20s` | time between status checks |
| `--sandbox`, `--harness`, `--engine`, `--image`, `--models`, `--session-root`, `--retention` | see [Runtimes](#runtimes) | local runtime |

```bash
replichai audit <paper-ref> --publish --detach
replichai finish my-lab/<repo> <paper-ref> --session-ref <ref> --invite <github-login>
```

### `replichai publish <artifact-dir> <paper-ref> [flags]`

The offline publishing path: it publishes an audit's artifact directory to a
filesystem forge, a directory on disk that stands in for GitHub, and never
touches the network beyond resolving the paper. It previews by default. Use it
to check what the redistribution gate would keep and what the manifest would
say.

| Flag | Default | Meaning |
|---|---|---|
| `--live` | off | write the repository instead of previewing it |
| `--out` | `published` | root directory of the filesystem forge |
| `--verdict` | `passed` | the verdict to record in the manifest: `passed`, `failed`, `exhausted`, `needs_revision`, `pending` or `unknown` |
| `--method` | discovered | path to a method directory |
| `--timeout` | `30s` | network timeout for resolving the paper |

## Signing in and the app

These commands work with a replichai web application. Without configuration
they talk to the hosted one.

| Command | What it does |
|---|---|
| `replichai login [--auth-url <url>] [--no-browser]` | signs the terminal in with a device code: it prints a URL and a code, opens the URL unless `--no-browser` is given, and waits for you to approve. `--auth-url` defaults to `$REPLICHAI_AUTH_URL`, then `https://auth.latere.ai` |
| `replichai whoami` | prints the signed-in identity and the organization context from the stored credential, without a network call |
| `replichai logout` | removes the stored credential |
| `replichai org [<org-uuid> \| --personal]` | prints the organization context the credential carries, or switches it to another organization or back to the personal context. Organizations belong to the identity service; the app does not group projects by them |
| `replichai project create <title>` | creates a project in the app and prints its id |
| `replichai project ls` | lists your projects |
| `replichai project sessions <project-id>` | lists a project's sessions: id, phase, repository, reference |
| `replichai project rm-session <session-id>...` | deletes sessions; each id is tried on its own, so one refusal does not stop the rest |
| `replichai push <project-file> --project <id>` | files the sessions of a local `run` in a project. Nothing is charged; pushing again updates the same rows |

The credential is stored in `~/.config/replichai/auth.json`, readable only by
you. `audit --publish --project` also goes through this sign-in, and so does
the `latere` runtime when neither `LATERE_API_KEY` nor `LATERE_TOKEN_COMMAND`
is set: before its calls to the Latere platform it asks Latere Auth for a
token that lasts a few minutes and is addressed to the platform, so the
stored credential itself is never sent there.

### Using your own deployment

Two variables point the terminal at a deployment other than the hosted one:

| Variable | Default | Read by |
|---|---|---|
| `REPLICHAI_AUTH_URL` | `https://auth.latere.ai` | `login`, `org`, and the token refresh and short-lived token request in `project`, `push`, `audit --publish --project` and the `latere` runtime. A sign-in made anywhere but Latere Auth cannot reach the Latere platform; set `LATERE_API_KEY` for that runtime instead |
| `REPLICHAI_API_URL` | `https://replichai.latere.ai` | `project`, `push` and `audit --publish --project` |

`whoami` and `logout` read only the stored credential and use neither.

The terminal's sign-in expects more of an identity provider than the browser's
does, so whether these commands work against a given deployment depends on the
provider it runs with; [`self-hosting.md`](self-hosting.md#the-terminals-sign-in)
lists what it needs. The commands that need no app (`audit` without
`--project`, `run`, `resolve`, `publish`, `finish`, `sessions`, `providers`,
`rates`) are unaffected, except that on the `latere` runtime they use the
sign-in as described above.

## Housekeeping

### `replichai sessions ls | prune [flags]`

Lists or removes the session directories local runs leave under the session
root. They are kept by default because a session's `workspace/` is the
reproduction.

| Flag | Default | Meaning |
|---|---|---|
| `--older-than` | `168h0m0s` | with `prune`: only remove sessions older than this |
| `--dry-run` | off | with `prune`: list what would be removed and remove nothing |
| `--session-root` | `$REPLICHAI_SESSION_ROOT`, then `~/.replichai/sessions` | where session directories are |

### `replichai models [--runtime <name>]`

Lists what `--model` accepts on a runtime: each Effort level, the stored
preference it is kept as, and the model it runs now, then every model the
runtime lets a session name exactly. On `latere` that is the platform's catalog
as it is offered to you.

```bash
replichai models --runtime latere
```

### `replichai providers`

Lists the installed runtimes and marks the one a command would use without
`--runtime`. It takes no flags.

### `replichai rates [flags]`

Checks that the rate card, the per-model price list a deployment charges
sessions at, names the models a runtime's tiers resolve to today. A tier that
resolves to a model the card does not name is charged at the most expensive
rates on the card, so the report says which tiers would be. Without a database
it reads the rate card compiled into the binary, which is what a new deployment
starts with.

| Flag | Default | Meaning |
|---|---|---|
| `--runtime` | `$REPLICHAI_RUNTIME`, then `anthropic-managed` | the runtime whose model resolution is checked |
| `--database-url` | `$DATABASE_URL` | read the rate card from this deployment's database instead |
| `--json` | off | print the report as JSON |

### `replichai help`

Prints the command list. `replichai <command> -h` prints one command's flags.

## Environment variables

Every variable the CLI reads is listed, with what it is for, in
[`example.env`](../example.env). The ones this page names:

| Variable | Used for |
|---|---|
| `REPLICHAI_RUNTIME` | the default runtime |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` | the `anthropic-managed` runtime |
| `REPLICHAI_GITHUB_TOKEN`, `REPLICHAI_GITHUB_OWNER`, `REPLICHAI_GITHUB_API_URL` | publishing |
| `REPLICHAI_AUTH_URL`, `REPLICHAI_API_URL` | signing in and the app |
| `LATERE_API_KEY`, `LATERE_TOKEN_COMMAND`, `LATERE_API_URL`, `LATERE_AUTH_URL` | the `latere` runtime, when it should not use your sign-in, and the platform's addresses |
| `REPLICHAI_SANDBOX`, `REPLICHAI_HARNESS`, `REPLICHAI_CONTAINER_ENGINE`, `REPLICHAI_HARNESS_IMAGE`, `REPLICHAI_MODELS_FILE`, `REPLICHAI_SESSION_ROOT`, `REPLICHAI_SESSION_RETENTION` | the local runtime |
| `DATABASE_URL` | `rates --database-url` |

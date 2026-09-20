# Changelog

Every tag has a section here, and that section is the body of the GitHub
release. A tag without one is refused at pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
moves those notes into a section for the tag, then commits, tags and pushes.

A section says what changed for the people who use the release, not what was
committed. The commit log already holds that.

## Unreleased

### Fixed

- Every json value the server writes now reaches its column as text rather
  than as bytes. Sent as bytes, such a value arrives as a hex literal that the
  column refuses with `invalid input syntax for type json`, but only when the
  driver sends parameters in the text format. The pooled connection string
  asks the server to describe each statement first, so the encoding is chosen
  correctly and no write was failing; the repair removes the dependency on
  that setting. It covers submitting a session, importing one run from your
  own machine, moving a session to its next phase, recording its latest
  status, and saving the platform settings that carry the rate card and the
  drafting model. ci-gate v0.46.0 fails a build that binds a json encoding as
  bytes, so the class cannot return.

## v0.30.0 - 2026-09-19

### Changed

- The server serves through the family's database pooler. Serving traffic
  opens `DATABASE_POOL_URL` when the deployment carries it and falls back to
  `DATABASE_URL`, so a deployment whose secret predates the pool starts
  unchanged. Migrations run at boot on the direct endpoint over a connection
  of their own and release it before the first request. The pool's own size,
  and no longer the replica count, is the server's claim on the shared
  database.

## v0.29.1 - 2026-09-18

- A release refuses to cut while this repository's CI is red, and the
  refusal says who acts: a budget or policy stop is the maintainer's, a
  flake is re-run, a code failure is fixed and pushed first (ci-gate
  v0.40.0). Nothing changes for a user of Replichai.
- The identity gate reads the frontend for the retired admin flag and
  refuses a second copy of the authorizer envelope (ci-gate v0.42.0).
  Nothing changes for a user of Replichai.

## v0.29.0 - 2026-09-13

- **`AUTH_AUDIENCE` names the one audience the server's bearer path accepts**
  and is required whenever `AUTH_ISSUER` is set; the deployment sets
  `replichai.latere.ai`. `REPLICHAI_TOKEN_AUDIENCE` and its default are gone.
  The CLI reads its own credential through the shared decoder, and the
  verifier runs the family's `authkit/conformance` suite in the server's tests.

- **A session from before session references were kept can no longer take
  the server down.** Cancelling such a session, or restarting the server with
  one still in publishing, called an engine that was never wired and panicked;
  because the row stayed active, every restart panicked again. Every session
  now goes through the one managed controller, and a fault inside one run
  fails that run, with its cause recorded, instead of the process.
- **A paper reference typed into the app can only reach public addresses.**
  The server fetched whatever a reference pointed at, followed every redirect,
  and read the whole body into memory. It now refuses loopback, private and
  metadata addresses when dialling, follows at most five redirects, and caps
  the body; the terminal fetches through the same rules. Every request body is
  bounded at the route, including the webhook's, and a project accepts at most
  fifty papers per request, resolved a few at a time.
- **A stranger learns nothing about a private project.** Two of the five ways
  a route resolved a project answered a guessed id with 403, which confirmed
  the project existed. All project and session routes now answer 404 to anyone
  who may not view the project, and only someone inside it is told what
  permission they lack.
- **An imported session belongs to the person who pushed it.** A member of one
  project could re-push another person's session id and move their record; a
  re-push now finds only its pusher's own row and never changes the project.
- **Signing in twice at once no longer refuses you.** The app sends more than
  one request when the login callback lands; the second could read the grant
  the first had just bound as belonging to somebody else.
- **A session cookie expires on the server's clock**, not only the browser's,
  so a copied cookie stops working when its twelve hours are up.
- **A local `replichai publish` includes `verdict.json`.** The deliverables
  are one list owned by the method, the publish gate admits exactly that list,
  and the run's instructions name every file on it.
- **`run --resume` retries into the repository the first attempt created**,
  and a project file's `publish.owner` applies (the flag first, then
  `REPLICHAI_GITHUB_OWNER`, then the file). `REPLICHAI_RUNTIME` selects the
  runtime for every CLI command, as the README said. `replichai <command> -h`
  lists that command's flags, generated from one registry so the help cannot
  drift from the code.
- **A GitHub refusal reports what GitHub said.** A 422 is treated as "already
  exists" only when GitHub says so, a rate limit is reported as retryable with
  its wait time, and a file over a megabyte reads as present.
- **A freeze override ends with the payment that clears the debt**, and a read
  no longer writes: every mutating request used to issue an update to retire
  an override that almost never existed.
- **The public registry and the sitemap are built once every thirty
  seconds** for anonymous callers, and immediately when a publication changes.
- **A run on your own machine no longer sees your shell's environment.** A
  stage started with everything the terminal had exported, every API key and
  cloud credential included, handed to an agent running with permissions
  bypassed. It now starts from a short allowlist plus what the run sets, and
  an exported Anthropic key can no longer silently override the login you
  chose. The harness's own configuration is read only by a session running
  on that login, the grant is written into the run's environment record, and
  Claude Code's `agents`, `commands` and `skills` are withheld like the rest
  of your standing instructions. A run no longer contacts telemetry hosts.
- **Two sessions on one machine cannot share a workspace.** The server runs
  several jobs against one local runtime; two asking at once were given the
  same directory. A session is now named when asked about and created when
  started, so each runs in its own directory.
- **A local session's cost is no longer under-counted**, and its last lines
  are no longer lost: the final event was read after the stage had been seen
  to exit. A transcript line over eight megabytes no longer hides every line
  after it. A finished stage is reaped rather than left as a zombie, and
  stopping a run that ignores the first signal ends it. A rejected
  evaluation ends `failed` on every runtime, where the local one used to say
  `done`.
- **The paid end-to-end runtime tests are opt-in** (`REPLICHAI_E2E=1`); they
  used to run whenever the tools happened to be installed.
- **The session screen no longer reconnects its event stream on every
  frame.** The stream is opened once per session and closed when the run
  ends; before this, a running session's page reopened it many times a
  second for as long as the page was open.
- **The app is usable from the keyboard.** Project, registry, file, verdict
  and session rows can be reached and opened with Tab and Enter; a dialog
  takes focus, keeps Tab inside it, and returns focus when it closes.
- **A malformed profile address, a stale language setting, or a server that
  answers with a page instead of data** no longer blank the app or report
  your connection as down; a refused rename, restore or member removal is
  shown rather than swallowed; an open transcript card stays on its event
  when the filter changes. Dates and amounts follow the app's language, not
  the browser's.
- **Deployment.** A local `.env` never enters the image; the pod runs as a
  numeric non-root user with every capability dropped; environment-specific
  values live in the overlays, so staging no longer inherits production's
  client id and admin list; `example.env` names every variable the server and
  the CLI read, and a test keeps it that way. The docs describe the CLI, the
  rubric, the method files and the release pipeline as they are.

## v0.28.4 - 2026-09-11

- **A session row says which run it is.** Under a paper, every row printed
  that paper's own reference, so a repeat study, the one view that produces
  several rows per paper, showed the same string on each. The row now carries
  the model the session ran on, and names the repository it made rather than
  labelling the link "GitHub".
- **A paper's runs take less room.** Five runs at a paper's own row height
  pushed the next paper off the screen; the nested rows are tighter, and the
  transcript link drops to its icon where the word repeated down a column.

## v0.28.3 - 2026-09-11

- **A run on your own machine can publish what it produced.** Every local
  `--publish` run was refused at the last step: the repository was created
  with a commit already in it, and a workspace that was never cloned from
  anywhere cannot add to that history. Runs of an hour were graded and then
  lost. New repositories are now created empty, and what the system writes
  into them it writes after the push.
- **Codex runs on your own login now get the method.** On the login the
  documentation tells you to use, the agent was started with no standing
  instructions at all, so it wrote its reproduction into a subdirectory,
  committed nothing, and left an empty repository behind a passing grade.
- **A dependency tree no longer stops a reproduction from being published.**
  A link into an installed package tree failed the whole harvest, which is
  the step that pushes the work.
- **A session row names its own repository.** Every reproduction of one paper
  shares a slug and differs by four characters, so repeat runs rendered as
  identical rows; the row now shows which repository it made.
- **Sessions can be listed and deleted from the terminal.** `replichai
  project sessions <id>` and `replichai project rm-session <id>...`, for the
  runs your own machine filed.
- **A failure says what actually failed.** Reading a file from the workspace
  was reported as the paper's source being unreachable.

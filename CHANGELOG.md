# Changelog

Every tag has a section here, and that section is the body of the GitHub
release. A tag without one is refused at pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
moves those notes into a section for the tag, then commits, tags and pushes.

A section says what changed for the people who use the release, not what was
committed. The commit log already holds that.

## Unreleased

## v0.31.0 - 2026-10-16

### Added

- Sessions can run on the Latere platform, on any model the platform serves.
  `replichai audit <paper> --runtime latere --model thorough` runs a graded
  audit as a hosted agent session, charged to your own Latere wallet; sign in
  with a personal access token in `LATERE_API_KEY`. A deployment offers it
  beside its default runtime with `REPLICHAI_RUNTIMES=latere`.
- Starting a session in the app, you choose where it runs and how hard it
  works: one picker on the paper offers the deployment's runtimes, an Effort
  level, and on the Latere platform a specific model. A session on Latere runs
  as you, after you sign in to Latere once, and your own Latere wallet pays.
- `--model` takes an Effort level, `quick`, `thorough` or `sustained` (the
  names `fast`, `balanced` and `best` still work), or one exact model as
  `provider/id`; `replichai models --runtime <name>` lists what a runtime
  accepts.
- A session started in the app records the runtime and model it was started
  on and who pays for it. A session the person pays for on the Latere platform
  takes nothing from the project's credits and shows the cost the platform
  reported.
- `replication.json` lists every model that served a session when more than
  one did, as a Latere Effort level can when the platform moves a turn.
- A run that did not succeed prints how it ended.

- A command reference, `docs/cli.md`, covering every command and flag, and a
  guide to running your own deployment, `docs/self-hosting.md`, covering
  Postgres, any OpenID Connect issuer, publishing to your own GitHub
  organization, and the optional payments and mail.
- A security policy, `SECURITY.md`, saying how to report a vulnerability
  privately and which controls the tests hold to their word, with a code of
  conduct, a citation file, and issue and pull request templates.
- A project file can run one paper more than once. `runs: 3` on the file or on
  one paper starts three sessions of it, each with its own repository and its
  own output directory (`<paper>-run-2`, and so on), and listing a paper twice
  means the same as `runs: 2`. The report counts runs and names the run beside
  a repeated paper.
- `replication.json` says which run it was. A `run` block records the project
  and the run's number among the runs of its paper, the runtime, the coding
  agent and its version, the model that ran and the one that was asked for, the
  sandbox and the network it could reach, a digest of the prompt it was given,
  when it started and finished, how it ended, how many times it was graded, and
  what it used. Manifests written before this still read.
- The findings pane shows every disagreement a claim has with the paper, not
  one: each names the quantity it compares, the paper's value and the run's,
  and a note on the size or cause of the gap.
- "What the authors released" marks a place the run could not look at, because
  the sandbox refused the address or the fetch failed, as "Not fetched". It used
  to have no way to say so, apart from reporting that nothing was found.
- Every run of a paper in `replichai run` reads the same copy of it. Before any
  run starts, each paper is fetched once (arXiv's PDF, the PDF the paper's page
  names, or an open-access copy), kept in `papers/` beside the project file,
  and handed to every run read-only and outside the repository. A paper whose
  publisher refuses automated downloads can name its copy with `file:` in the
  project file. A project with a paper that has no copy stops before anything
  runs and names the paper, and a resumed project hands its remaining runs the
  copies its first runs read.
- A paper in a project file can list what its authors released, under
  `artifacts:`. Each is snapshotted once before any run starts, a repository as
  a git bundle of its branches and tags with the commit its default branch
  pointed at, anything else as the file its address serves, and every run of
  the paper is handed the same snapshots, read-only and outside the repository.
  An artifact that cannot be snapshotted, or an address that serves a web page
  instead of a file, stops the project before anything runs and is named.
- Every run of a project is given a named network profile: `artifacts`, the
  list of places papers and their code and data live, which is the default, or
  `open`. Set it with `network:` in the project file, for the file or for one
  paper, or with `--network`. A sandbox that cannot enforce the profile refuses
  the run rather than running it under another.
- `replication.json` names the network profile a run was given, the digest of
  the paper copy it read, and the source, commit and digest of each artifact
  snapshot it was handed, as `network_profile`, `paper_digest` and `artifacts`.
- A run on your own machine keeps `verdict.json` as each grading round left it,
  and what replichai found when it read it. A published run commits them
  beside the grades as `grade/findings-<n>.json` and `grade/checks-<n>.json`;
  round 0 is the run's first attempt, before any revision.

### Changed

- The site has a new design, in the same ink and type as the rest of Latere.
  Every label is set in plain sentence case rather than monospace capitals, and
  Inter is shipped rather than assumed. The landing is set like a paper:
  numbered sections, captioned tables with plain rules, and published
  reproductions as a list of references. The particle animation on the first
  screen is replaced by a drawn figure of the two loops, which runs only while
  it is on screen and can be paused. A verdict leads with its outcome and its
  per-component table, and its overall rate is a footnote under that table. The
  logo's outer loop is a single open arc. The sign-in button appears once on
  screen at a time. A trackpad pull stretches only the pane under it, never the
  whole frame.
- `verdict.json` is at schema 2. A claim carries a `mismatches` list in place
  of one `mismatch` object. Each entry holds the two values as text and, when
  each is a single number, as numbers in `reported_value` and `observed_value`,
  with a `unit` and a `note`. `released` accepts `not_fetched`. Findings
  written under schema 1 still read, and the app shows them the same way.
- The method changed with the schema, so its digest moved. A reproduction made
  from this release on is not comparable under one digest with an earlier one.
- On your own machine, replichai reads `verdict.json` against its schema before
  each grading round, and tells the judge and the next revision what it found.
  A round whose findings replichai cannot read no longer passes, whatever the
  judge wrote: the run is revised, or fails once it has no revision left.
- The method's first step reads the paper copy a run was given instead of
  fetching another, so the method's digest moved again.
- `replichai run` needs a runtime that can hand a run a file it cannot change,
  which the local runtime can and the hosted one cannot. On the hosted runtime
  its runs are refused before a session starts.
- The footer links the product catalog at `latere.ai/products` under Company,
  and lists the chat as Latere Chat under Applications.

### Fixed

- A session on the Latere platform reaches every file it left, not only the
  first page of its folder.
- A run that wrote no `instrument.json` now says the rubric's Instrument
  criterion requires one. It used to name criterion 7, which is Findings as
  data, and to say the run failed when the grader had not said so.
- `--max-revisions 0` grades a run once and never revises it. It used to be
  read as the default of three. A negative value is refused, and the hosted
  runtime, whose grader cannot be told zero, refuses a run that asks for none.
- Menus and select lists open in front of the whole page against the control
  that opened them, flip above it where there is no room below, and stay
  inside the window, so no box around them cuts them off.
- Resuming a project that lists one paper several times runs the runs that
  failed or never started. It used to match by paper alone, so it ran none of
  them and reported every run as done with the first run's repository.
- Two runs of one paper started together no longer take each other's
  repository or write into the same output directory.
- A run that fails before it starts leaves a manifest in the repository that
  was created for it, saying it did not start. That repository used to stay
  empty, with nothing in it to say why.
- One field of the wrong type in `verdict.json` no longer costs the whole
  file. The findings are read row by row, the problem names the row, and the
  rest is kept. Before, such a file was reported as not valid JSON, which was
  false, and nothing in it was read. In a 49-run pilot, 14 of the 27 findings
  files read when the runs were finalized were lost this way; of the 29 files
  those runs published, 27 now read.

### Security

- Built with Go 1.27.2 and golang.org/x/net v0.60.0, which fix GO-2026-6611, GO-2026-6612, GO-2026-6613 and GO-2026-6617.

## v0.30.3 - 2026-10-08

### Changed

- `latere.ai/x/pay` v0.2.0. Nothing changes for a payment: checkouts were already in USD, and the off-session charge whose 3-D Secure case v0.2.0 fixes is not one this service makes.
- OpenTelemetry Go v1.46.0, with the log modules at v0.22.0, the slog bridge at
  v0.20.1 and otelhttp at v0.71.0, past GO-2026-6615 and GO-2026-6505, and
  `latere.ai/x/pkg` v0.90.2. pkg v0.90.2 names the service resource with
  semantic conventions v1.43.0, the schema of this SDK; with an older schema
  the two conflict when merged and the server disables telemetry export at start.

- The footer at the end of the landing page is redrawn as the footer every
  Latere site ends in, from latere-ui v2.0.0. A lead block holds ReplicHAI's
  own lockup, as the header sets it, icon links to Latere's Slack, LinkedIn, X
  and GitHub profiles, and an icon button that opens a menu of the Light, Dark
  and System appearances, in place of the three-way appearance control. The
  language select is gone with no menu in its place, since the app is in
  English only. Four link columns sit beside it: Applications (Latere and Gallery)
  with Research (ReplicHAI) below it, Platform (Latere Platform, Identity),
  Company (About, Why Latere, Blog, Open Source, Contact) and Legal (Trust
  Center, Privacy, Terms, Impressum), each under a semibold heading in the
  full text tone. ReplicHAI is now listed in the footer. Drive, Topos, Cella
  and Lux no longer have entries of their own: they are sections of the
  platform console, which the one Latere Platform link opens. The tagline and
  the Community column are gone. Below 1024px the lead block moves above the
  columns, and on a phone the columns sit two across, above it.

### Fixed

- On Linux the host sandbox's readiness check finds Bubblewrap by its program,
  `bwrap`, and names it so when it is missing. It looked for an executable
  named `bubblewrap`, which no package installs, so a machine with Bubblewrap
  installed was refused.

## v0.30.2 - 2026-09-27

### Fixed

- A release no longer fails requests at the moment an old pod stops. The pod keeps serving for five seconds after it is told to stop, until the ingress has stopped sending it traffic; before, requests in that second could get a 502.

## v0.30.1 - 2026-09-25

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
- `--sandbox container` no longer falls back to
  `ghcr.io/latere-ai/replichai-harness:latest`, an image that was never
  published, so a run without `--image` failed at a pull that could not
  succeed. A run that names no image (by `--image` or
  `REPLICHAI_HARNESS_IMAGE`) is now refused before the engine is touched, and
  the message says to build a harness image and name it.
- The deploy manifests no longer name `ghcr.io/latere-ai/replichai:latest`.
  The base carries the placeholder tag `unreleased`, which is never pushed,
  and `deploy/prod` pins the release it runs, which `lateregate release` now
  moves to each new tag. Applying `deploy/prod` by hand runs that release
  rather than whatever was published last, and a release no longer rolls
  production through `:latest` before setting its own tag.

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

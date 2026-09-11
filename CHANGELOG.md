# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

## v0.28.4 - 2026-09-11

- **A session row says which run it is.** Under a paper, every row printed
  that paper's own reference — the same string on each, in the one view a
  repeat study produces. The row now carries the model the session ran on,
  and names the repository it made rather than labelling the link "GitHub".
- **A paper's runs take less room.** Five runs at a paper's own row height
  pushed the next paper off the screen; the nested rows are tighter, and the
  transcript link drops to its icon where the word repeated down a column.
- **A session from before session references were kept can no longer take
  the server down.** Cancelling one, or restarting with one left in
  publishing, reached for an engine that was never wired and panicked; with
  the row still active, every restart panicked again. Every session now goes
  through the one managed controller, and a fault inside one run fails that
  run with its cause recorded rather than the process.
- **A paper reference typed into the app can only reach public addresses.**
  The server fetched whatever a reference pointed at, followed every redirect,
  and read the whole body into memory. It now refuses loopback, private and
  metadata addresses at dial time, follows at most five redirects, and caps
  the body; the terminal fetches through the same rules. Every request body is
  bounded at the route, the webhook's included, and a project takes at most
  fifty papers per request, resolved a few at a time.
- **A stranger learns nothing about a private project.** Two of the five ways
  a route resolved a project answered a guessed id with 403, confirming it
  existed; all project and session routes now answer 404 to anyone who may
  not view the project, and only someone inside it is told what they lack.
- **An imported session belongs to whoever pushed it.** A member of one
  project could re-push another person's session id and move their record;
  a re-push now finds only its pusher's own row and never changes project.
- **Signing in twice at once no longer refuses you.** The app fires more than
  one request when the login callback lands; the second could read the grant
  the first had just bound as somebody else's.
- **A session cookie expires on the server's clock**, not only the browser's,
  so a copied cookie stops working when its twelve hours are up.
- **A local `replichai publish` includes `verdict.json`.** The deliverables
  are one list the method owns, and the publish gate admits exactly that
  list; the run's instructions name every file on it.
- **`run --resume` retries into the repository the first attempt created**,
  and a project file's `publish.owner` applies (flag, then
  `REPLICHAI_GITHUB_OWNER`, then the file). `REPLICHAI_RUNTIME` selects the
  runtime for every CLI command, as the README said. `replichai <command> -h`
  lists that command's flags, generated from one registry so help cannot
  drift.
- **A GitHub refusal says what GitHub said**, a 422 is only "already exists"
  when GitHub says so, a rate limit is reported as retryable with its wait,
  and a file over a megabyte reads as present.
- **A freeze override ends with the payment that ends the debt**, and a read
  no longer writes: every mutating request used to issue an update to retire
  an override that almost never existed.
- **The public registry and the sitemap are built once every thirty
  seconds** for anonymous callers, and at once when a publication changes.
- **Deployment.** A local `.env` never enters the image; the pod runs as a
  numeric non-root user with every capability dropped; environment-specific
  values live in the overlays, so staging no longer inherits production's
  client id and admin list; `example.env` names every variable the server and
  the CLI read, and a test keeps it that way. The docs describe the CLI, the
  rubric, the method files and the release pipeline as they are.

## v0.28.3 - 2026-09-11

- **A run on your own machine can publish what it produced.** Every local
  `--publish` run was refused at the last step: the repository was created
  with a commit already in it, and a workspace that was never cloned from
  anywhere cannot add to that history. Runs of an hour were graded and then
  lost. New repositories are created empty, and what the system writes into
  them it writes after the push.
- **Codex runs on your own login now get the method.** On the login the
  documentation tells you to use, the agent was started with no standing
  instructions at all — so it wrote its reproduction into a subdirectory,
  committed nothing, and left an empty repository behind a passing grade.
- **A dependency tree no longer stops a reproduction from being published.**
  A link into an installed package tree failed the whole harvest, which is
  the step that pushes the work.
- **A session row names its own repository.** Every reproduction of one paper
  shares a slug and differs by four characters, so repeat runs rendered as
  identical rows; the row shows which repository it made.
- **Sessions can be listed and deleted from the terminal.** `replichai
  project sessions <id>` and `replichai project rm-session <id>...`, for the
  runs your own machine filed.
- **A failure says what actually failed.** Reading a file from the workspace
  was reported as the paper's source being unreachable.

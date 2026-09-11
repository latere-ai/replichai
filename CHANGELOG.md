# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

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

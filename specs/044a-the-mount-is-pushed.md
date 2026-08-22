# 044a — The mount is pushed, not just committed

**Status:** designed.

**Scope:** the mount protocol's push instruction, and a rubric criterion that grades the pushed state. No code outside `internal/audit` and `method/`.

**Depends on:** [044](044-a-run-that-produced-nothing.md), [006f](006f-taking-a-reproduction-out.md).

## Problem

`mountProtocol` (`internal/audit/audit.go`) tells the agent:

> Commit your progress with git as you finish each part — small, meaningful commits — and run "git push" so the history is preserved.

One sentence, one clause, and the push is the subordinate half of it. In the incident run the agent read it, committed five times, and never pushed once. Nothing else in the run mentions pushing: the rubric grades eight criteria and every one of them is satisfied by a file existing in the sandbox, so a run with a perfect local tree and an empty remote scores eight out of eight. The grader sent the agent back to revise its verdict twice and never once told it its work was not published.

An unpushed commit is not evidence. When the sandbox is released it is not anything.

## What changes

**The protocol states the push as its own step, with the reason.** The mount is not a working directory that happens to be under version control; it is the published surface, and the local branch is not it. The instruction becomes: commit *and* push as one act, after every part; verify with `git status` that the branch is not ahead of `origin/main`; and know that anything left unpushed is discarded when the session ends.

**The rubric gains criterion 9, `Published`.** Every other criterion is evidence in a file, and this one is too: the run records the output of `git status -sb` and `git log --oneline origin/main -1` — showing the branch level with its remote, and naming the commit the remote holds. A recorded status showing the branch ahead of `origin/main` fails the criterion, and the grader's revision loop is what makes the agent push. This grades *publication*, never reproducibility: a paper that turns out to be unreproducible passes criterion 9 by pushing that finding.

The method digest changes, which is correct and is what the digest is for — a run graded against nine criteria is not comparable against one graded against eight.

## Acceptance

- `mountProtocol` names pushing as a separate step, states that unpushed work is discarded, and asks for a `git status` check.
- `method/rubric.md` carries criterion 9, phrased as evidence in the output files, and scoped to publication rather than reproducibility.
- `method/instructions.md`'s output section names the push among the things the run owes.
- A test pins that the mount protocol mentions the push and the discard consequence, so the sentence cannot be edited back down to a clause.

## Not in scope

Enforcing the push mechanically — that is 044b's recovery path, and the umbrella records why neither the platform nor the grader can reach into the sandbox to run `git push` itself.

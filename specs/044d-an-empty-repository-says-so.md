---
title: "An empty repository says so"
status: built
---

# 044d — An empty repository says so

**Status:** built.

**Scope:** `FinalizeLive` distinguishes a reproduction with unreadable findings from a repository with no reproduction in it, and both the manifest and `UNVERIFIED.md` say which.

**Depends on:** [044](044-a-run-that-produced-nothing.md), [021a](021a-the-findings-as-data.md), [044b](044b-the-server-harvests-the-session.md).

## Problem

The incident's repository ended with this, and nothing else, as the record of a twenty-minute $2.21 run:

```markdown
## Findings not recorded as data (system)

`verdict.json` could not be read, so this reproduction's boundary table, hidden
decisions, and scorecard exist only as prose in this repository:

- the run did not write verdict.json
```

Every word is true and the paragraph is misleading. *"exist only as prose in this repository"* is a claim the repository does not support: there is no prose, there is no reproduction, there is nothing but the two files the system wrote before the agent started. A reader, and the manifest, cannot tell this from a good run that wrote its findings in Markdown and skipped the JSON.

`readFindings` asks one question, whether `verdict.json` is readable, and the answer is the same `false` in both cases. The distinguishing fact is one the finalize already holds and never examines: the committed tree.

## What changes

`FinalizeLive` already lists the tree for `VerifyTree`. It classifies the same list once more:

```
tree paths ─┬─▶ VerifyTree              → leaked
            └─▶ holdsReproduction(paths) → empty?
```

`holdsReproduction` asks whether any path in the tree is something other than what the system itself put there: `.replichai/paper.json`, the auto-initialized `README.md`, `UNVERIFIED.md`, and the manifest and transcript paths a re-finalize will have added. A false answer means the run committed nothing.

**The manifest records it.** `Findings` gains `Reproduction bool`: whether the repository holds a reproduction at all. A manifest is still written: the run happened, it cost money, and the transcript is the evidence of what it did. What changes is that the manifest no longer implies a reproduction exists.

**`UNVERIFIED.md` states what is the case.** When the tree holds no agent-authored path, the system's section states that the run committed nothing to the repository and that the transcript is the only record of it, instead of a sentence about a boundary table that is not there. When the tree does hold a reproduction, the existing wording is correct and is kept.

**Ordering.** This reads the tree that 044b's harvest has already contributed to, so a run recovered through the output directory is not reported as empty. The two specs share one ordering rule: harvest, then finalize.

## Acceptance

- A finalize over a tree holding only the system's own files reports the run as having committed nothing, in the manifest and in `UNVERIFIED.md`.
- A finalize over a tree holding agent work with no `verdict.json` keeps today's findings wording exactly.
- A finalize over a tree holding agent work and a valid `verdict.json` writes no `UNVERIFIED.md` section, as today.
- A re-finalize of an empty repository — where the manifest and transcript are now in the tree — still reports it as empty, and does not stack a second note.

## Not in scope

Failing the run, refusing the manifest, or refunding it. The record becomes accurate; what the operator does about an empty run is outside this spec.

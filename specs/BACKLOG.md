# Backlog

Feasibility questions and scope extensions worth getting right *eventually*, deliberately kept **out of the implementation roadmap** (the numbered spec map). An item large enough to need its own argument gets a deferred spec and a pointer from here — item 5 does. These are not committed work and not sequenced. They are recorded so they shape the model when the time comes, and so they are not lost to a hallway conversation. An item that becomes committed work leaves this file rather than gaining a note here: the meter-in-currency item did, as [011](011-what-a-session-costs.md).

The MVP proves the loop on **single-study, machine-runnable-inner-loop papers** — which the golden set (`evals/`) targets on purpose. The items below are the later-stage stress tests of the "audit / replication" model, and each is large enough to warrant its own investigation before any spec.

## 1. A paper is not one study

The engine and the 007 series lean on *paper → an inner loop → a reproduction*. Real papers — HCI especially — often carry **multiple studies**: chained (Study 2's design depends on Study 1's finding) or parallel (several studies triangulating one claim). This assumption is load-bearing and currently unquestioned.

Open questions:
- **What is the unit?** Paper, study, or experiment? The project model (006) can already hold multiple runs — does a paper map to one project with *N* studies, or to *N* reproductions? A paper→study hierarchy likely has to appear somewhere in identity/registry keying.
- **The audit decomposition assumes one inner loop.** A multi-study paper has several, possibly with different verdicts and rates. Does the manifest carry a per-study breakdown? Does `rate` become per-study? Does a paper get one roll-up verdict or a vector?
- **Ordering and dependency.** Chained studies mean a reproduction may need Study 1's output as Study 2's input — an intra-paper dependency the current flat model has no notion of.

## 2. Study-type variety, and instruments that resist replication

The 007 *instrument* abstraction assumes a study can be re-instantiated as a re-runnable thing. The variety of study types is vast, and some instruments cannot be reproduced mechanically at all:

- VR/AR apparatus, specialized or custom hardware, physical lab setups, wet-lab protocols, field deployments, longitudinal/in-the-wild studies.

Open questions:
- **A replicability spectrum**, not a binary: fully re-runnable → partially → apparatus-bound → not mechanically replicable. The 007 instrument/experiment model presumes an instrument *exists* to bind data to; the non-replicable end needs an honest verdict rather than being forced into that model.
- **Where the line sits** between "the instrument *is* the outer loop (needs people or hardware)" and "the instrument is a re-runnable artifact." The inner/outer thesis already answers part of this — a VR apparatus arguably falls on the outer, non-machine side — but 007 has not reconciled its instrument model with that boundary.

## 3. The golden set has no fully-servable study (007a eval gap)

007a's servability assessment ([`specs/007a`](007a-the-study-protocol-as-an-artifact.md)) is judged against both golden cases, but both discriminate in **one direction only**. `case-01`'s outer loop studies a visual interface the reproduction never rebuilds (fails `apparatus_recovered`); `case-02`'s prospective-memory task is servable but its feed manipulation is not (fails `page_administrable`). Neither has an outer loop that is servable end to end.

So a change making the audit *credulous* about servability shows up immediately in both, but a change making it *uselessly conservative* — marking a genuinely hostable study unservable — shows up in neither. Until a third case is added, chosen for a fully hostable screen-based study (single session, browser measures, page-administrable manipulation, apparatus recovered), the conservative direction is unguarded. This is the honest state of the regression set, recorded rather than patched over by asserting a `true` the current papers do not support.

## 4. A published verdict names people who never agreed to it

The public zone (006, 007) assumes an owner may publish *"this paper's inner loop is 40% reproduced"* with the authors' names attached and the transcript as evidence. The system-authored verdict ([`docs/procedure.md`](../docs/procedure.md) §8) makes that record **honest**. Honesty is not standing, and nothing in the design gives the subject of a verdict anything to do about it.

Open questions:
- **Right of reply.** Can an author attach a response to a published run, visible on the same page? Does that make them a participant in a project they do not own, and what does that do to the access model (006c, 006n)?
- **Correction and supersession.** We ran the wrong commit, or missed a data file the authors would have pointed us to. Who can trigger a re-run — only the owner? Does a corrected verdict *replace* the wrong one, or supersede it with the original still reachable? 006o withdraws a publication; it does not revise one.
- **What the artifact claims for itself.** A verdict nobody is obliged to accept still needs to state its own status precisely: what was run, on what, and what it does *not* assert. Closer to a lab notebook than a ruling, and the wording is load-bearing.

This is not hypothetical caution. [`docs/landscape.md`](../docs/landscape.md) found it twice from independent directions: the frontier labs avoid audit products because being a researcher's co-pilot and their auditor are incompatible postures, and the incumbents' real edge was **mandate** — the standing to be the entity whose verdict counts — rather than technology.

Backlog rather than roadmap because nothing is public yet, so nothing is currently wrong. It becomes blocking the first time a run naming a third party leaves the owner's project.

## 5. A paper the world cannot fetch

Every paper the platform can hold is one that already exists publicly, because `paper.Key` is offline over an immutable public reference. A draft, or a manuscript under submission, has no DOI, no arXiv id, and no URL — and is the case where a reproduction is worth the most, since a failing inner loop is still fixable before it becomes a published record. Written up at ideation level as [016](016-a-paper-that-is-not-published-yet.md), which names the forks and resolves none.

The short version of why it is not a storage feature:

- **A mutable document has no stable identity.** 006d's rule for an identifier-less PDF is `sha256` of the extracted text, so v1/v2/v3 of one manuscript are three unrelated papers to `project_papers`, to the double-trigger guard (which therefore lets two versions run at once), and to the registry. This is 012's unsolved arXiv-versus-DOI divergence promoted to the normal case.
- **The good outcome makes it worse.** A draft that gets published acquires the DOI it should always have had, and the sessions already run against it must follow — a merge of two paper keys, which nothing does today.
- **A paper and a bundle of materials are different things.** An audit handed its code and data is not measuring what an audit that had to find them measured; one `rate` cannot answer both questions honestly.
- **Deletion collides with a rule that is right.** 012 makes a paper with sessions permanently undeletable, deliberately. "Delete my manuscript" is a request that must be honourable. Both cannot hold.

Storage itself is the easy part and is already forked cleanly: `experiment.ArtifactStore` is the seam, and below it either drive ([008](008-drive-data-plane.md), which already claims this ground) or a `replichai/` prefix in the shared bucket.

## Why these are backlog, not roadmap

1–3 are end-to-end "get the auditing/replication model right" concerns — valuable, but far past what the MVP needs to prove. Validating them means test papers the golden set does not yet contain (multi-study, hard-instrument). 4 is a different kind of deferral: not hard to reason about, just not yet live, and it turns from a design question into an incident the day it is skipped. 5 is deferred for a third reason again: it is wanted, and the storage half is a week's work, but the identity half rewrites indexes the whole product keys on — so building the obvious part first would mean discovering the model by migration.

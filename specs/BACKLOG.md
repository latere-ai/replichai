# Backlog

Feasibility questions and scope extensions worth getting right *eventually*, deliberately kept **out of the implementation roadmap** (the numbered spec map). These are not committed work and not sequenced. They are recorded so they shape the model when the time comes, and so they are not lost to a hallway conversation.

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

## Why these are backlog, not roadmap

They are end-to-end "get the auditing/replication model right" concerns — valuable, but far past what the MVP needs to prove. Validating them means test papers the golden set does not yet contain (multi-study, hard-instrument). Recorded here to be picked up as a deliberate later-stage investigation, each likely its own spec, once the single-study loop is solid.

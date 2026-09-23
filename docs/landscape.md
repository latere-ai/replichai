# Landscape

Who else is building something like this, as of July 2026.

Every entry below was checked against a live source with a date. Where a search came back
empty, the empty result is recorded too. "No funded competitor exists in this niche" is a
finding we depend on, and recording it saves re-deriving it.

## The four axes

Many products sound adjacent to this one and are not. Four questions separate them, and
every player below is scored on the same four:

1. **Audit, or generate?** Does it render a judgment on work someone already published, or
   does it produce new research?
2. **Who authors the verdict?** A model writing a report about itself, or a system outside
   the sandbox reading what was actually committed ([`procedure.md`](procedure.md) §8)?
3. **Does it simulate the outer loop?** Does it sell simulated participants, refuse to, or
   does the question not arise?
4. **Is there a durable artifact for one real paper?** A repository with the agent's own
   commit history and its transcript, or a number on a leaderboard?

## What the map shows

**Nobody in production executes the paper.** The integrity industry is real, funded and
multi-year: Papermill Alarm (Clear Skies, in Editorial Manager and ScholarOne), Argos
(Scitility, 40k papers flagged high-risk by Oct 2024), ImageTwin and Proofig on 150M-image
corpora, Cactus Paperpal Preflight at APS and Frontiers, Signals ($1.1M seed from ACS
Publications, reported May 2026). Every one of them reads text and images. None re-runs a
line of the paper's code. The statistical-consistency tools (statcheck, GRIM, SPRITE,
JATSdecoder, and the new [metacheck](https://www.scienceverse.org/metacheck/), beta,
5 Jun 2026) are text-only by construction. Google's Paper Assistant Tool
([reported 30 Jun 2026](https://theaiinsider.tech/2026/06/30/google-ai-tool-finds-flaws-in-scientific-papers-before-peer-review/),
piloted at STOC and ICML) flags flaws in proofs and experiment design and explicitly does
not execute anything.

The market has spent a decade establishing that a machine may render a verdict on a
published paper. It has not yet established that the machine may run it.

**The institutions that could have the mandate have not moved.** AEA's Data Editor, cascad,
CODECHECK, ReScience C, AJPS/Odum (median 20-day human turnaround), the Biostatistics
reproducibility editor, ACM's artifact badging: all active, all bound by human labor, and no
LLM agent found in any of their published processes. The one exception is
**[Code Ocean](https://codeocean.com/blog/trusted-agents)**, which shipped an agent (Aqua,
Oct 2025) into a platform that already holds verification contracts with Nature-family,
Springer and SAGE. Aqua runs analyses with logged provenance; humans still gate the
"verified reproducible" badge. That is one product decision away from being a competitor.

**Corrected 25 Aug 2026.** Aqua is **not** a replication agent. It is a general
in-platform research assistant for the user's own work: it does not audit a third
party's paper and it does not gate the badge. The "one product decision away"
reading above rests on a misreading of what shipped, and the paragraph should be
rewritten before it is used in a pitch.

**The generation-side labs are working on a different thesis, for structural reasons.**
Claude Science (relaunched 30 Jun 2026), Gemini for Science (I/O, May 2026),
FutureHouse/Edison Scientific (Kosmos, $70M seed at $250M), Sakana, Lila Sciences ($550M
raised), Periodic Labs, Autoscience, Intology, AI2 Asta. None ships a "verify this published
paper" feature. Asta names experiment replication as unbuilt future work; Kosmos shows
recreating prior findings as a validation demo, not a product. The reason is not technical.
Their revenue rewards generating claims, and an audit product requires publishing
evidence-backed negative verdicts against named external work. A lab cannot be both the
researcher's co-pilot and the researcher's auditor. This confirms that Claude for Science is
adjacent rather than competing.

**Deriving a hosted study from a paper's protocol is unclaimed.** No hosting platform
(jsPsych, JATOS, lab.js, Pavlovia, Gorilla, Labvanced, Testable, Prolific, CloudResearch)
generates an experiment from a published paper's extracted protocol. Gorilla's "AI
Components" are an in-study chatbot task, not a paper-ingestion pipeline. The single
closest artifact is a Qualtrics case study (Jan 2026) where a human manually rebuilt one
published survey in the builder and paired it with synthetic responses. [007d](../specs/007d-hosting-a-study.md)
is open ground.

**The inverse thesis is far better funded than ours.** Simile (emerged 2026, $100M Series A
led by Index; founders Joon Sung Park, Percy Liang, Michael Bernstein; angels Fei-Fei Li and
Andrej Karpathy) commercializes the Park et al. generative-agent work at enterprise scale.
Aaru raised a Series A at a ~$1B headline valuation (Dec 2025). Viewpoints AI and Evidenza
market synthetic panels using borrowed academic validation language ("Stanford-validated,
88% match"). They sell: skip the people, simulate the population, get the answer today.
The correction sits in preprints with no commercial packaging. Hullman et al.
([arXiv 2602.15785](https://arxiv.org/abs/2602.15785), 2026) distinguish heuristic
substitution from calibrated validation, and Yeykelis et al.
([arXiv 2408.16073](https://arxiv.org/abs/2408.16073)) built 19,447 AI personas to
re-run 133 experiments from 14 *Journal of Marketing* papers.

**Corrected 25 Aug 2026.** This paragraph previously said that study "matched up to 81% of
real effects while also producing significant effects for up to 83% of null findings". Both
figures were wrong, and the second inverted the paper's meaning. Checked against the PDF:
81% appears nowhere, and 83% is the *success* rate on strongly significant findings ("with
83% of the findings (54 out of 65) successfully replicated", for p < 0.001), not a
false-positive rate on nulls.

What the paper actually reports is a better argument for
[007](../specs/007-from-reproduction-to-replication.md)'s "synthetic pilot, never evidence"
rule than the invented number was, because it is a pattern rather than one alarming
figure. Personas reproduce 76% of original main effects (84/111) and 68% overall including
interactions (90/133), but accuracy tracks how strong the original effect was:

| Original finding | Replicated by personas |
|---|---|
| p < 0.001 | 83% (54/65) |
| p > 0.5 (i.e. originally null) | 55% (6/11) |
| Main effects | 76% |
| Interaction effects | 27% |

The authors' own reading: personas "may be prone to both false positives and false
negatives, indicating a potential risk when using them to detect or confirm less pronounced
effects", and while they "may sometimes accurately identify the absence of an effect, they
also risk introducing spurious findings".

That is the citable case. A synthetic cohort is most reliable on effects so strong they were
never in doubt, where it is least needed, and least reliable on weak effects, null results,
and interactions, which are what a pilot exists to test. Nobody can know in advance which
regime a new study is in, so the pilot can never be the evidence.

**Corrected 25 Aug 2026: HCI is no longer unclaimed.** Adar, Lee, Lei, Liao & Peng,
*Revibing Code from Papers: Reimplementing HCI Artifacts*, UIST 2026
([arXiv 2608.00450](https://arxiv.org/abs/2608.00450), 1 Aug 2026) reimplements HCI artifacts
from papers and releases 27 of them. It also draws the inner/outer boundary explicitly, which
this document elsewhere claims nobody does: the machine rebuilds the interactive system, and
participants are needed for the study that system is a baseline for. It stops at the artifact
and neither runs the participant half nor authors an independent verdict. The paragraph below
was written before it existed and is now false as stated.

**HCI was unclaimed until Aug 2026, and the reason is a small corpus.** [On the Computational
Reproducibility of HCI](https://dl.acm.org/doi/full/10.1145/3772318.3791129) (CHI 2026,
13 Apr 2026) found that only **76 CHI papers have ever shared both data and code**; 37 of
those (49%) fully reproduced. Roughly **1% of CHI papers share reproducible materials at
all**, against 31% in *Cognition*. RepliCHI ran 2011–2014 and has no successor; CHI's
artifact badge is self-asserted with no external review. No agent-driven reproduction tool
for HCI was found anywhere. ML and economics already have pilots running, so HCI is late
rather than early.

The consequence: a corpus where 1% of papers share materials is a small inner loop to sell.
It is also why the boundary table and "nothing found is a finding" matter more here than
they would in ML. For most CHI papers, the audit's product is the absence of materials.

**Manual replication's price floor is known from one program.** Reproducibility Project:
Cancer Biology spent **~$52.5k–75k per paper over 8 years**, and cut scope from 53 papers to
23 because authors would not cooperate ([Nature](https://www.nature.com/articles/d41586-021-03691-0),
[IFP cost analysis](https://ifp.org/how-much-should-we-spend-on-replication/)). DARPA SCORE
closed in April 2026: 3,900 papers sampled, ~50% replication success, and data available for
only 24%. We could not find a published per-paper cost for any other program (not
Psychology, not Many Labs, not SCORE). That absence is itself informative: the field appears
to have no unit-economics discipline, and a platform can supply one.

## The closest three

**[VERITAS](https://arxiv.org/abs/2607.02931)** (Liu, Tjiaranata & Tan, U. Chicago,
3 Jul 2026). Abstract read directly. "Given a paper, a code repository, or both, VERITAS
extracts the paper's claims, runs the methodology while resolving issues as they arise, and
judges each claim against the evidence from experiment runs", returning "an
importance-weighted Replication Score, a severity-rated log of every fix applied, and the
patched codebase", evaluated on 65 papers "spanning computer science, social science,
medicine, and astrophysics". It states its own gap plainly: "no general-purpose replication
tool exists."

Architecturally this is our loop, and it is the closest comparison we have. *How we differ:*
it is a CLI research artifact with no ownership model and no publication surface; its fix log
is a record of repairs, not the agent's full transcript published as the evidence behind a
verdict; it is domain-agnostic where we are deliberately not; and it takes "a paper, a code
repository, or both", which presumes released code, an assumption that fails for roughly
99% of CHI papers. The abstract makes no mention of a human-versus-machine boundary or of
sensitivity to how a paper is decomposed into claims. Both are absences from the abstract
rather than confirmed absences from the paper; read the full text before relying on either
in a pitch.

**[I4R's AI Replication Engine](https://i4replication.org/the-ai-replication-engine-first-experiments-and-whats-next/)**
(Institute for Replication, ongoing, NeurIPS 2026 target). Three agents (reproducibility,
error detection, robustness) re-executing empirical economics and social-science code
against published results, benchmarked on 250+ I4R Games papers. *How we differ:* they have
the institutional mandate we lack and we have the product layer they lack; they are
economics, we are HCI; and their pipeline assumes released code exists, which in HCI it
usually does not.

**[Code Ocean](https://codeocean.com/blog/trusted-agents)** (Aqua shipped Oct 2025). The only
commercial platform with both an execution agent and standing verification contracts with
publishers. *How we differ:* Aqua helps an author run their own work with provenance; it
does not adjudicate a third party's, and Code Ocean's badge is still human-gated. They are
the incumbent whose verdict would carry weight on the day they choose to automate it.

Other research systems: **ReproRepo** (CMU, Jun 2026, 1,149 real ML papers,
ground truth from actual GitHub issues), **[Agentic Replication Package Quality
Evaluation](https://arxiv.org/abs/2606.02006)** (Jun 2026, 51 machine-verifiable criteria,
evidence-grounded reports on real packages), **[coding agents replicating sci-ML
papers](https://arxiv.org/abs/2607.02134)** (Jul 2026, per-claim evidence tracking),
**[AutoReproduce](https://github.com/AI9Stars/AutoReproduce)** (ACL 2026, mines cited
references for implicit implementation detail, a technique we should adopt for step 4),
and the live **[ICML 2026 Agent Reproduction Challenge](https://huggingface.co/spaces/ICML-2026-agent-repro/challenge)**
(15 Jul – 2 Aug 2026, $4,000 in GPU credits, bring your own coding agent, claims from every
ICML 2026 paper, logbooks reviewed by organizers rather than scored automatically, and
coverage acknowledged upfront as "uneven" depending on code and data availability). One
sweep reported ~6,800 papers and 20,594 logged verdicts; neither number survived a direct
check of the writeup, so treat them as unconfirmed. The window closes 2 Aug 2026 and the
logbooks are the field's only public record of what agents actually achieve on real papers
at scale. Capture them before they are archived. The benchmark
set proper (PaperBench, CORE-Bench, SUPER, REPRO-Bench, SciReplicate-Bench, PRBench,
ReplicatorBench, LMR-Bench, ResearchCodeBench, AutoExperiment) measures agents. It does not
produce an artifact for a paper anyone owns. These are measurement competitors, not product
competitors: they are where our method should be benchmarked, not where our product competes.

## Who could ship this next quarter

**Code Ocean**, by extending Aqua from "run this for the author" to "certify this for the
editor". Platform exists, contracts exist, agent exists. This is the most likely entrant.

**cascad and CODECHECK**, by putting an agent underneath existing human sign-off. Their
bottleneck is reviewer hours, not authority, and triage is the natural first use. They would
keep the trust and shed the cost.

**AI2 Asta**, the most plausible lab pivot: nonprofit, no commercial exposure to a "this does
not replicate" finding, and replication already named on the roadmap. It has fewer resources
than the frontier labs, which is the only thing slowing it.

**Nobody else, quickly.** AEA, AJPS/Odum and ACM hold the strongest mandates and show zero
public AI exploration; economics and political science are cautious by disposition and ACM's
badging is federated per-conference, so central policy change is slow. The frontier labs have
the capability and the wrong incentive.

## What this changes for us

**Our verdict-authorship claim is weaker than it reads.** System-authored, deterministic
verdicts are already the norm across CORE-Bench, REPRO-Bench, PRBench and especially "Read
the Paper, Write the Code" (strict information isolation, deterministic cell-level diffing).
Among benchmarks, axis 2 is the baseline, not a differentiator. What is actually ours is the
*combination*: a system-authored verdict over an agent-authored claim, for one specific real
paper, with the transcript published as the evidence behind it. The differentiator is
[`procedure.md`](procedure.md) §8 as a delivered artifact, not as a design principle.

**Three things are unclaimed.** The inner/outer boundary as a first-class, defended output:
nothing in the field draws it, and every benchmark sidesteps it by living in ML, where the
question does not arise. Deriving a hosted participant study from an audited paper's protocol
([007d](../specs/007d-hosting-a-study.md)). And HCI as a domain.

**Our known weakness is already public.** [`procedure.md`](procedure.md) §7 records that the
same paper decomposes into five components or seven across two runs, swinging the rate from
40% to 79%. VERITAS ships an importance-weighted Replication Score without, as far as the
paper shows, that caveat. Our treatment of the rate as a footnote to the table is more
defensible and less marketable. Make that trade deliberately rather than discovering it later.

**Publishing a negative verdict against named work is an open question we have not asked.**
This research is the first evidence that better-funded players consider it a real risk: the
frontier labs avoid audit products for exactly this reason, and the incumbents' edge was
described as mandate rather than technology, meaning the standing to be the entity whose
verdict counts. Our public zone ([006](../specs/006-web-application.md),
[007](../specs/007-from-reproduction-to-replication.md)) assumes an owner may publish "this
paper's inner loop is 40% reproduced" with the authors' names attached, and nothing in the
design yet addresses author right-of-reply, correction, or the standing of a verdict nobody
is obliged to accept. That is a spec-level question, not a paragraph, and it is unresolved.

**The buyer is not yet a funding line.** No 2025–26 funder targets "AI agent audits
reproducibility" as a category. The nearest fits are COS (infrastructure, NSF POSE-eligible),
Sloan's Metascience & AI stream, Renaissance Philanthropy, the UK metascience grants
(£6M pool, round 2 opened Feb 2026), and NIH's new central replication resource (Feb 2026)
as an adopter rather than a funder. We would be defining a line rather than entering one.

## Searched, found nothing

Recorded so it need not be re-searched: no funded startup whose product is agentic paper
reproduction sold to researchers; no Product Hunt launch in the category; no Chinese,
Japanese or European entrant (Chinese-language searches returned only the same English
arXiv papers); no defunct player under this branding; no agent-driven reproduction tool for
HCI specifically; no hosting platform deriving a study from a paper.

Two name collisions to remember: **RepliBench** (UK AISI) measures whether a *model*
can self-replicate, an AI-safety eval, unrelated. **UL Solutions' AI Algorithm
Reproducibility Program** certifies that a commercial product repeats its claimed outcome:
same word, different market.

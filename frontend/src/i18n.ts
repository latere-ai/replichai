// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useMemo, useState } from 'react'

// English only for now. To add a language, add its code to the Lang union and a
// matching block to `strings`. The translation key type TKey is derived from
// the English block below, so TypeScript requires the new block to translate
// every string and a locale cannot ship half-done. A language switcher is then
// a two-line change.
export type Lang = 'en'

// I18n is what a component needs to render text: the chosen language, a setter,
// and the translate function. It lives here rather than in App so that the
// error-copy module can type against it without importing the whole app.
export type I18n = ReturnType<typeof useI18n>

// en is the source of truth: every key the product uses, in English. A new
// locale is typed against its keys, so it cannot omit one.
// A `|` in a heading is a seam: a place where the writer allows the line to
// break. Everything between two seams is rendered unbreakable, so a heading
// breaks at a clause and never mid-phrase. CSS cannot make that decision: it
// breaks at whatever space overflows, and `text-wrap: balance` only equalises
// the resulting line lengths. A translator moving the sentence into another
// language moves the clause with it, which no stylesheet can follow. Only
// headings carry seams; body copy wraps wherever it needs to.
const en = {
    // auth
    // The landing (017). The claim is a question because that is the question
    // the product answers, and the lead says what it does that nothing else in
    // the field does: it runs the paper. The two titles label the inner/outer
    // boundary in the hero legend, which is shown to readers before they have
    // an account. The card promises only what 006q made true: signing in is
    // open and a session is what spends.
    landingClaim: 'Does the paper|reproduce?',
    landingLead:
      'Give ReplicHAI a paper. It finds what the authors released, implements and re-runs the part a machine can run, records every decision the paper does not state, and returns a verdict with its evidence.',
    landingInnerTitle: 'The inner loop',
    landingInnerBody:
      'The algorithm, model or analysis pipeline. ReplicHAI implements it, runs it, and publishes it as a repository with the agent’s commit history.',
    landingOuterTitle: 'The outer loop',
    landingOuterBody:
      'The study that tests it with people. ReplicHAI names it, marks it out of scope, and never simulates it. Simulating a human study is a common way reproducibility claims go wrong.',
    landingVerdictTitle: 'The verdict',
    landingVerdictBody:
      'Written by the system, signed by a named researcher, and published with the full transcript, so you can check the reasoning rather than only the number.',
    // The landing as a place (029): the section headings, the figure captions,
    // and the close. The claim, the lead, and the three points above are 017's
    // and are reused verbatim; the argument did not change, only the space it
    // has.
    lpLoopsTitle: 'One loop re-runs.|The other cannot.',
    lpLoopsCaption:
      'The algorithm can be re-run, so it is drawn as a closed loop. The study with people cannot, so it is drawn open. ReplicHAI never simulates it.',
    lpPipelineTitle: 'What a session|does',
    lpPipelineCaption:
      'Four steps. Each leaves something you can open afterwards: the release it found, the repository it wrote, and the transcript of how it got there.',
    lpStepPaper: 'The paper',
    lpStepPaperBody: 'The link is resolved to one paper key, so two people auditing the same paper audit the same record.',
    lpStepPaperArtifact: 'A paper key',
    lpStepRelease: 'What the authors released',
    lpStepReleaseBody: 'Code, data, licenses and preregistration are looked up and recorded, including what is missing.',
    lpStepReleaseArtifact: 'A release record',
    lpStepRerun: 'The re-run',
    lpStepRerunBody: 'The part a machine can run is implemented and executed. Every decision the paper does not state is written down.',
    lpStepRerunArtifact: 'A repository, committed as it runs',
    lpStepVerdict: 'The verdict',
    lpStepVerdictBody: 'What reproduced, what did not, and what could not be judged, listed component by component.',
    lpStepVerdictArtifact: 'A manifest and a transcript',
    lpAnatomyTitle: 'A verdict you can|check',
    lpAnatomyCaption:
      'Three parts, kept separate on purpose. A single number would have to be trusted. Kept apart, the claim, the evidence and the verdict can each be checked.',
    lpPartClaim: 'The claim',
    lpPartClaimBody: 'What the agent says it reproduced, and how.',
    lpPartClaimBy: 'the agent',
    lpPartEvidence: 'The evidence',
    lpPartEvidenceBody: 'The full session transcript, committed next to the work rather than summarized.',
    lpPartEvidenceBy: 'the record',
    lpPartVerdict: 'The verdict',
    lpPartVerdictBody: 'Written by the system from the claim and the evidence, and signed by a named researcher.',
    lpPartVerdictBy: 'the system, signed',
    // Who signed the reproduction, said as that. A bare name under a paper's
    // title is read as the paper's byline, and the person here is the opposite
    // of an author: they are the one who re-ran somebody else's work and put
    // their name to what came back.
    lpCardSigner: 'Reproduction signed by {name}',
    lpStripTitle: 'Published reproductions',
    lpStripCaption: 'Signed, public, and readable without an account. Open one to see what a finished audit looks like.',
    lpCloseTitle: 'Read one|before you sign up.',
    lpCloseBody: 'Everything published here is public: the verdict, the repository, and the transcript that produced them.',
    // The rebuild (033). 029's copy above is the argument and is reused
    // untouched; everything here belongs to a band 029 did not have. The
    // eyebrows name what a band is about. A reader scanning the page reads
    // only those, so each one is a noun, not a verb.
    lpEyebrowAudited: 'Reproducibility audits',
    lpStepN: 'Step {n}',
    lpStripEyebrow: 'The registry',
    lpHeroAside: 'The inner loop is re-run. The outer loop is not.',
    lpLoopsEyebrow: 'The boundary',
    lpPipelineEyebrow: 'A session, step by step',
    lpAnatomyEyebrow: 'In the app',
    // What comes back: the files a reader opens first. The names are filenames
    // and are not translated.
    lpShipsEyebrow: 'What you get',
    lpShipsTitle: 'A repository|you can clone.',
    lpShipsCaption:
      'An audit writes a reproduction repository you can clone. Each file answers one question, and one of them lists everything that could not be confirmed.',
    lpShipsReadme: 'What the paper is, what the reproduction is, and how to run it.',
    lpShipsRepro: 'The verdict with its per-component table, the inner/outer boundary, the unstated decisions, and the open-science scorecard.',
    lpShipsSrc: 'A runnable best-effort implementation of the inner loop.',
    lpShipsUnverified: 'Everything that could not be confirmed, each with what blocked it.',
    lpShipsSources: 'The paper’s key and every artifact search performed, with its result.',
    lpShipsVerdictJson: 'The verdict as data: each component, its outcome and its evidence, for a program to read.',
    lpShipsInstrument: 'The study as data: its protocol, the entry point of the analysis in src/, and whether a machine could serve the study.',
    // The walkthrough's plates: the artifact each step leaves, drawn as the app
    // draws it. The paper key is deliberately unresolvable; see lpDemoExample.
    lpPlateKey: 'Paper key',
    lpPlateKeyValue: '10.0000/replichai.example',
    lpPlateKeyNote: 'One paper, one key, one record to audit.',
    lpPlateRelease: 'Release record',
    lpPlateCode: 'Code',
    lpPlateData: 'Data',
    lpPlatePrereg: 'Preregistration',
    lpPlateFound: 'Found',
    lpPlatePartial: 'Partial',
    lpPlateNone: 'None',
    lpPlateCommitted: 'Committed as it runs',
    lpPlateCommit1: 'implement inner loop from §4.2',
    lpPlateCommit2: 'record unstated seed and tolerance',
    lpPlateCommit3: 'add UNVERIFIED.md with 3 blockers',
    lpPlateManifest: 'Manifest',
    lpPlateSigned: 'Signed by a named researcher. Transcript committed next to the work.',
    // The verdict panel. It is the strongest thing this product has to show and
    // it is also a fabrication, so it says so: an Example marker, and a paper key
    // under the unregistered 10.0000 prefix that resolves to nothing. A made-up
    // verdict against a real DOI is the exact failure this product exists to
    // catch, and it would be on our own front page.
    lpDemoTitle: 'Verdict',
    lpDemoExample: 'Example',
    lpDemoPublished: 'Published',
    lpDemoOutcome: 'Partly reproduces',
    lpDemoSummary:
      'The inner loop reproduces on the released code, with one numeric mismatch in Table 3 that the run recorded and did not adjust. The outer loop is out of scope: the effect rests on 84 participants, and the run does not simulate them.',
    lpDemoColClaim: 'Claim',
    lpDemoColLoop: 'Loop',
    lpDemoColOutcome: 'Outcome',
    lpDemoColEvidence: 'Evidence / blocker',
    lpDemoInner: 'Inner',
    lpDemoOuter: 'Outer',
    lpDemoVerified: 'Verified',
    lpDemoPartial: 'Partial',
    lpDemoNotScored: 'Not scored',
    lpDemoRow1: 'Ranking model beats the BM25 baseline',
    lpDemoRow1Evidence: 'Re-ran on the released split; nDCG@10 within tolerance.',
    lpDemoRow2: 'Reported ablation gain of 4.1 points',
    lpDemoRow2Evidence: 'Observed 2.6. The paper reports 4.1, the run gives 2.6.',
    lpDemoRow3: 'Participants preferred the assisted condition',
    lpDemoRow3Evidence: 'Rests on 84 participants. Not simulated.',
    // Who it is for. Named as fields rather than as job titles: the thing that
    // decides whether this product fits is the shape of the paper, not the
    // department it came from.
    lpWhoEyebrow: 'Who it is for',
    lpWhoTitle: 'Papers whose evidence|comes from people.',
    lpWhoCaption:
      'Built for research with human participants. A paper of that kind almost always contains two loops, and the audit’s job is to place the boundary between them precisely and keep to it.',
    lpWhoHci: 'Human–computer interaction',
    lpWhoHciBody: 'A system paper whose contribution is the system and whose evidence is a user study.',
    lpWhoPsych: 'Psychology',
    lpWhoPsychBody: 'An analysis pipeline a machine can re-run on data a cohort produced once.',
    lpWhoSocial: 'Social science',
    lpWhoSocialBody: 'A model and a survey instrument, published together and checked separately.',
    lpWhoElse: 'And anywhere else',
    lpWhoElseBody: 'a finding rests on what people did, and the outer loop is named and left open.',
    // What it costs. What 006q and 011 made true and nothing more: no prices.
    // The rate card is a database row behind an admin endpoint, and a number
    // typed here would be a number that drifts.
    lpCostEyebrow: 'What it costs',
    lpCostTitle: 'You pay|per session.',
    lpCostCaption:
      'There is no seat or plan. A session is charged for the tokens it used, and whatever it does not spend is returned.',
    lpCostSignIn: 'Sign in',
    lpCostSignInBody: 'An account holds your papers, the sessions you run on them, and the verdicts you sign.',
    lpCostFund: 'Fund a project',
    lpCostFundBody: 'Credits you buy go into your wallet. You allocate them to a project, and sessions spend from the project.',
    lpCostRun: 'Run',
    lpCostRunBody: 'A reserve is held while a session runs. Whatever it does not spend is returned when it finishes.',
    // Figure 1 and the two tables (047). A figure is numbered and captioned the
    // way a journal sets one: the number, a title that states the claim, then
    // what each panel shows. The panel letters are the caption's handles.
    figAlt: 'Figure 1. An inner loop, a closed ring that a marker goes round once per run, inside an outer loop, an open ring that nothing travels.',
    figImplement: 'Implement',
    figRerun: 'Execute',
    figCompare: 'Compare',
    figRecord: 'Record',
    figRunLabel: 'Run',
    figOpen: 'Never simulated',
    figPanelA: 'a',
    figPanelB: 'b',
    figLabel: 'Figure {n}.',
    figTitle: 'The boundary every audit draws.',
    figBody:
      '(a) The inner loop: the analysis a machine can re-run on what the authors released. Each lap is one run. (b) The outer loop: the study with people. It is named and left open, and ReplicHAI never simulates it.',
    figPause: 'Pause the figure',
    figPlay: 'Play the figure',
    tableLabel: 'Table {n}.',
    lpShipsTable: 'What every session commits to its repository.',
    lpShipsColFile: 'File',
    lpShipsColAnswers: 'What it answers',
    lpDemoTable: 'A verdict as the app shows it. The paper and its numbers are invented.',
    // The rate is derived from the rows and set as the table's footnote: the
    // product never leads with one number a reader would have to trust.
    lpDemoFootnote: 'Derived from all nine components, three of them shown above: 5 verified, 2 partial, 2 blocked (72%).',
    lpPlateBlocked: 'Blocked',
    lpNavRegistry: 'Registry',
    // The offer of the app's source that the AGPL asks a network service to
    // make (source.tsx). Shown only once the repository is public.
    sourceCode: 'Source code (AGPL-3.0)',
    signInTitle: 'Sign in',
    signInBody: 'Sign in to submit a paper, watch the audit run, and sign the verdict.',
    signInNote: 'Built for research with human participants.',
    signInWith: 'Sign in with Latere',
    authUnavailableTitle: 'ReplicHAI|is unavailable',
    authUnavailableBody: 'We couldn’t check your session. You may be offline, or the service may be temporarily unavailable.',
    retry: 'Try again',
    // Sign-in is open (006q), so this screen is no longer "wait for an admin".
    // It is not only "you were banned" either: a session minted before the
    // change, an address the identity provider did not return, and an address
    // already bound to another account all land here too. Naming a ban would
    // accuse somebody of something that did not happen, so the copy says only
    // what is certain, that there is no access right now, and offers the two
    // things that help.
    noAccessTitle: 'No access|to this account',
    noAccessBody:
      'You are signed in as {email}, but this account cannot use ReplicHAI right now. Sign out and back in. If that does not help, a platform admin has revoked access and can restore it.',
    // shell
    workspace: 'Workspace',
    navProjects: 'Projects',
    navPublic: 'Public registry',
    newProject: 'New project',
    foldSidebar: 'Collapse sidebar',
    unfoldSidebar: 'Expand sidebar',
    // On a phone the rail is a drawer rather than a column, so the same head
    // control closes it and a hamburger in the topbar brings it back (030).
    openNav: 'Open navigation',
    closeNav: 'Close navigation',
    settings: 'Settings',
    navSettings: 'Settings',
    settingsAccount: 'Account',
    settingsProfile: 'Profile',
    access: 'Access',
    loading: 'Loading…',
    // platform admin (006k)
    platform: 'Platform',
    navAdmin: 'Admin',
    adminIntro:
      'Everyone who has signed in, their tier, and their wallet balance. Anyone can sign in: an account appears here on its first login, with no credits until you add some. You can grant a tier or add credits by email before someone signs in; they take effect when they do. Revoking bans an account. It stays listed so you can undo that.',
    platformPolicy: 'Platform policy',
    savePolicy: 'Save policy',
    startReserve: 'Start reserve (USD)',
    overdraftCap: 'Overdraft floor (USD)',
    overdraftHint:
      'How far below zero a project may go before a running session is stopped. A session normally runs to completion once started, because stopping it midway wastes what it has spent. This limit only catches a runaway session; the start reserve is what stops the next one. Project members do not see this value.',
    rateCardHint: 'What a session is charged, per million tokens. A model not on the list is charged the highest rates on it.',
    rateModel: 'Model',
    rateInput: 'Input',
    rateOutput: 'Output',
    rateCacheRead: 'Cache read',
    rateCacheWrite5m: 'Cache write, 5 min',
    rateCacheWrite1h: 'Cache write, 1 hour',
    // Editing the card (the price list in Platform policy). A new row is blank,
    // so the console never picks a price; each message names what keeps the
    // card from being saved, the same rules the server applies.
    rateAdd: 'Add a model',
    rateRemove: 'Remove {model}',
    rateModelPlaceholder: 'Model id, as the vendor names it',
    rateEmpty: 'The card needs at least one model.',
    rateNoModel: 'Give every row its model id, as the vendor names it.',
    rateTwice: '{model} is listed twice.',
    ratePrice: 'Give each price of {model} as dollars per million tokens: a number, zero or more.',
    rateAllZero: '{model} is priced at zero for everything, and a row like that is ignored, so the model is charged the highest rates. Give it its prices.',
    startReserveHint:
      'How much a project must have available before a session can start in it. The amount is held while the session runs and returned when it finishes, so it limits how many sessions one project can run at once. It is not the final cost, which is charged from the tokens the session used.',
    policyUpdatedBy: 'Last changed by {who}',
    people: 'People',
    noGrants: 'Nobody has signed in yet.',
    grant: 'Grant',
    revoke: 'Revoke',
    grantHint: 'Anyone can sign in and browse. Running a session needs credits. Grant sets someone’s tier; revoke bans them. Admins can also set platform policy.',
    topup: 'Add credits',
    topupPrompt: 'Credits to add to {email}’s wallet, in US dollars. They can then allocate credits from the wallet to a project.',
    walletOf: 'What {email} has left in their wallet to allocate to a project.',
    adminError: 'That change was not saved. Try again in a moment.',
    adminNotAdmin: 'Only a platform admin can change this.',
    // credits (011)
    walletTitle: 'Your credits. A project needs {reserve} available before a session can start. Allocate credits from here to fund one.',
    // Nothing to spend and nothing ever spent: a new account, not one that has
    // run down. Showing \u201c$0.00\u201d to somebody who never had any reads as a
    // loss rather than a starting state.
    walletEmpty: 'no credits yet',
    walletEmptyTitle: 'This account has no credits yet. Ask a platform admin to add some, or work in a project somebody else has funded.',
    errNoCredits: 'This project is out of credits. Allocate more to it to continue.',
    errNoCreditsAtAll: 'You have no credits yet. Ask a platform admin to add some, or work in a project somebody else has funded.',
    // buying credits (022e)
    beta: 'Beta',
    navWallet: 'Wallet',
    walletHeading: 'Your wallet',
    walletDesc: 'Credits you buy and spend. Sessions spend from the project you allocate credits to, not from the wallet.',
    topUp: 'Top up',
    topUpWallet: 'Top up wallet',
    topUpTitle: 'Top up your wallet',
    topUpChoose: 'Choose an amount to buy. You can top up again later.',
    topUpOther: 'Other',
    topUpMinHint: 'US dollars, minimum {min}.',
    topUpSecure: 'You pay on Stripe’s checkout page. EU customers are billed in euros. Credits arrive in your wallet as soon as the payment clears.',
    topUpAmount: 'Amount in US dollars',
    topUpContinue: 'Continue to payment',
    topUpPayCta: 'Pay {total}',
    topUpRedirecting: 'Redirecting…',
    walletActivity: 'Recent activity',
    walletActivityCapped: 'Showing your most recent activity.',
    ledgerCardTopup: 'Card top-up',
    ledgerRefund: 'Refund',
    topUpPay: 'You pay',
    topUpFee: 'Fee ({pct}% + {fixed})',
    topUpFeeWhy: 'Covers card processing and platform costs. The rest becomes credits at the compute price.',
    topUpGet: 'Credits added',
    topUpMin: 'The minimum top-up is {min}.',
    topUpProcessing: 'Payment received. Your credits will appear here shortly.',
    errPaymentsOff: 'Card payments are not set up on this deployment yet.',
    errBelowMin: 'That is below the minimum top-up.',
    checkoutFailed: 'Couldn\u2019t start the payment. Try again in a moment.',
    // the frozen account (022d/022f)
    frozenBanner: 'A refund or chargeback left your balance negative, so your account is view-only. Top up to lift the hold.',
    frozenControl: 'Your account is on hold. Top up to lift it.',
    errFrozen: 'Your account is on hold after a refund left the balance negative. Top up to lift it.',
    frozen: 'Frozen',
    unfreeze: 'Unfreeze',
    unfreezeTitle: 'Let this account work again while it still owes. The waiver ends once the balance is settled.',
    refreeze: 'Re-freeze',
    refreezeTitle: 'This account was allowed to work while still negative. Re-freeze to put the hold back.',
    onlyOwing: 'Only owing',
    noOwing: 'No accounts have a negative balance.',
    solvency: 'Outstanding liability',
    solvencyTitle: 'Credits sold and not yet spent on compute: the sum of every wallet and project balance. Compare it with what the vendor account holds.',
    // a project's own credits, and the two moves on them
    projectCredits: 'Credits',
    creditsBalance: 'Balance',
    creditsAvailable: 'Available now',
    creditsAvailableTitle:
      'What is still free to spend. It is lower than the balance while sessions are running, because each holds the start reserve until it finishes. That is why a project with credits can still refuse a new session.',
    creditsEnough: 'Enough to start a session. {reserve} is held while one runs, and whatever it does not spend is returned.',
    creditsShort: 'Not enough to start a session. This project needs {reserve} available. Allocate more from your wallet.',
    creditsAmount: 'Amount in US dollars',
    creditsAllocate: 'Allocate',
    creditsReclaim: 'Take back',
    errAllocateShort: 'Your wallet doesn’t have that much to allocate. Top up to add more.',
    errReclaimHeld: 'That much is not free to take back yet. Credits held by a running session are returned when it finishes.',
    errMoveNotLead: 'Only a project lead can take credits back out.',
    errMoveNotMember: 'You are not a member of this project.',
    errMoveAmount: 'Enter an amount above zero.',
    errMoveFailed: 'That transfer failed. Try again in a moment.',
    sessionCostTitle: 'What this session cost its project.',
    sessionCostRunningTitle: 'What this session has cost so far. It is charged when the session ends.',
    signOut: 'Sign out',
    back: 'Back',
    // appearance: three choices, not a toggle. "auto" follows the operating
    // system, and the other two pin it regardless of what the machine does.
    theme: 'Appearance',
    themeAuto: 'Auto',
    themeAutoHint: 'Auto · follow the system',
    themeDark: 'Dark',
    themeLight: 'Light',
    // projects
    projectsIntro:
      'A project holds one or more papers and every replication session run on them. Each session is a record you can inspect, with its steps and token usage. It is not a score for ranking papers.',
    statementEmpty: 'Nothing yet. Credits you buy, or an admin adds, show up here.',
    ledgerTopup: 'Added by an admin',
    ledgerAllocate: 'Allocated to a project',
    ledgerReclaim: 'Taken back from a project',
    ledgerOther: 'Adjustment',
    // The named forms (024b): a prefix the project name follows, linked when the
    // project still exists and plain text once it is purged.
    ledgerAllocateTo: 'Allocated to',
    ledgerReclaimFrom: 'Taken back from',
    // The merged list (018). Ownership is shown in the row rather than being a
    // place to look, so the owner of your own project reads "You", the common
    // case, which nobody should have to read a name to recognize.
    colPaper: 'Paper',
    colStatus: 'Status',
    colVisibility: 'Visibility',
    ownerYou: 'You',
    ownerUnknown: '—',
    noProjects: 'No projects yet. Create one to start an audit.',
    // The boundary band (034). It replaces three counters, two of which were the
    // list below them counted. This is the product's primary number, and it is
    // never a bare rate: a count, its denominator, and what the remainder is.
    evidenceEyebrow: 'Published evidence',
    evidenceAcross: 'across {n} projects',
    evidenceOf: 'of {n} sessions committed a repository',
    evidencePublished: '{n} published',
    evidenceNothing: '{n} without a repository',
    creditsEyebrow: 'Your credits',
    creditsInWallet: 'in your wallet',
    creditsNote: 'Sessions spend from the project you allocate credits to, not from the wallet.',
    // The filter (034). 018 removed the mine/shared tabs because which of the two
    // a project was is not a fact the reader chose. This is the opposite: a
    // narrowing the reader picks and can unpick, over rows already on screen.
    filterProjects: 'Filter by paper or owner',
    filterAll: 'All',
    filterRunning: 'Running',
    filterPublished: 'Published',
    noMatches: 'No project matches that filter.',
    // The Published column: what a project has actually handed over, said as a
    // fraction of what it has run rather than as a rate on its own.
    colPublished: 'Published',
    publishedNone: 'Nothing published yet',
    publishedOf: '{n} of {total}',
    publishedUnit: 'sessions',
    // The trash (019). Two steps, because the dangerous one has to be
    // deliberate: the row action is reversible and says so, and the permanent
    // one states what it does not remove: the repositories close, they are not
    // destroyed.
    download: 'Download everything',

    navTrash: 'Trash',
    trashIntro:
      'Projects you moved to the trash. They are hidden from you and your collaborators, and everything in them is kept until you delete them permanently.',
    noTrash: 'Nothing in the trash.',

    trashProject: 'Move to trash',
    trashTitle: 'Move this project to the trash?',
    trashBody:
      'It is removed from your list and your collaborators’ lists. Its papers, sessions and results are kept, and you can restore it from the trash at any time.',
    trashPublicNote: 'This project is public. Moving it to the trash withdraws the publication, makes its repositories private, and records the withdrawal.',
    trashConfirm: 'Move to trash',
    trashing: 'Moving…',
    errSessionsRunning: 'A session is still running in this project. Stop it first.',
    trashFailed: 'Couldn’t move this project to the trash. Try again in a moment.',
    restore: 'Restore',
    restoring: 'Restoring…',
    restoreFailed: 'Couldn’t restore this project. Try again in a moment.',
    purge: 'Delete permanently',
    purgeTitle: 'Delete this project permanently?',
    purgeBody:
      'The project, its papers, and every session it holds are removed from ReplicHAI and cannot be brought back.',
    purgeKeepsRepos:
      'The GitHub repositories its sessions published are made private, not deleted. This removes ReplicHAI’s record of the reproduction; the repositories are kept as evidence.',
    purgeConfirm: 'Delete permanently',
    purging: 'Deleting…',
    errCreditsRemaining: 'This project still holds credits. Take them back first, then delete it.',
    purgeFailed: 'Couldn’t delete this project. Try again in a moment.',
    trashedBanner: 'This project is in the trash. Nothing can be changed or started here until you restore it.',

    // Removing one session (032)
    deleteSession: 'Delete session',
    deleteSessionTitle: 'Delete this session?',
    deleteSessionBody:
      'The session and its record are removed from this project. Its cost stays on your ledger. The session cannot be brought back, but you can run the paper again.',
    deleteSessionKeepsRepo:
      'Its GitHub repository is made private, not deleted. This removes ReplicHAI’s record of the reproduction; the repository is kept as evidence.',
    deleteSessionConfirm: 'Delete session',
    deletingSession: 'Deleting…',
    errSessionRunning: 'This session is still running. Stop it first.',
    deleteSessionFailed: 'Couldn’t delete this session. Try again in a moment.',
    blockerDeleteHint: 'Delete it to publish the rest of this project.',
    sessionCount: '{n} session(s)',
    // A row cites the first paper a project holds, and counts the others (043).
    morePapers: '+{n} more',
    private: 'Private',
    public: 'Public',
    // public zone
    publicIntro:
      'Reproductions that their owners have signed off and made public. Each one includes its sessions and the full transcript that produced them, so you can check the reasoning rather than only the number.',
    noPublic: 'No public projects yet.',
    initiator: 'Published by',
    // What a reader with no account gets at an address that is not public (028).
    // The server answers one 404 for private, withdrawn, and never-existed, so
    // the copy claims none of them; it says what is certain and offers the two
    // things that help.
    notPublicTitle: 'Not public',
    notPublicBody: 'This address is not public. If you have been given access to it, sign in and you will return here.',
    notPublicBodyIn: 'This address is not public, or it is not shared with your account.',
    browseRegistry: 'Browse the registry',
    // An address this app does not have (042c). It says what happened without
    // guessing why, whether a typo, a truncated link, or a page that was never
    // here, and offers the two places worth going next.
    notFoundTitle: 'No page|at this address',
    notFoundBody: 'There is no page at this address on ReplicHAI. The link may be incomplete, or the page may never have existed.',
    notFoundHome: 'Go to your projects',
    // submit / new project
    submit: 'Submit',
    submitting: 'Submitting…',
    activeSession: 'This paper is already being replicated in this project.',
    submitNoSpend: 'Nothing runs or is charged until you start a session.',
    newProjectDesc: 'Name the project, add one or more papers, and invite collaborators. Each paper gets its own replication sessions.',
    projectNameLabel: 'Project name',
    projectNamePlaceholder: 'Optional. Left blank, the project is named after the first paper.',
    papersLabel: 'Papers',
    // Optional since 036: a create records papers and starts nothing, so the
    // container is useful before it has any. Said on the label, because a
    // reader deciding what to type is looking at the label, not at an error.
    papersOptional: 'Optional. You can add papers from inside the project.',
    paperPlaceholder: 'arXiv link · DOI · ACM DL',
    addPaper: 'Add another paper',
    removePaper: 'Remove',
    resolve: 'Look up',
    resolvingPaper: 'Looking up…',
    collaboratorsLabel: 'Collaborators',
    collaboratorsPlaceholder: 'Emails, comma-separated (optional)',
    collaboratorsHint: 'Invited to the project as collaborators.',
    createProjectBtn: 'Create project',
    creatingProject: 'Creating…',
    resolveFailed: 'We could not reach the site this paper is on. It may be down or busy. Try again in a moment.',
    unresolvablePaper: 'We don’t recognize this link. Paste a DOI, an arXiv link, or the paper’s page on a publisher site.',
    // One mistake with two fixes, so it names both (036).
    nameOrPaper: 'Name the project, or add a paper to name it after.',
    // The short forms sit directly under the paper they belong to, so they only
    // have to say what about it; the row already says which one.
    errUnresolvable: 'We don’t recognize this link.',
    somePapersNotRecognised: 'The project was created. We did not recognize the links marked below. You can add those papers again from inside the project.',
    createFailed: 'The project was not created because of an error on our side. Try again in a moment.',
    // Working inside a project: adding a paper costs nothing, starting one is
    // the only act that spends.
    papers: 'Papers',
    projectNoPapers: 'No papers yet. Add one to say what this project is about.',
    addPaperHint: 'Paste a link. Adding a paper only records it. Nothing runs until you start a session.',
    addPaperBtn: 'Add',
    addingPaper: 'Adding…',
    addPaperFailed: 'Couldn’t add that paper. Try again in a moment.',
    errPaperExists: 'This project already has that paper.',
    startSession: 'Start replication',
    startingSession: 'Starting…',
    // The model tier a session runs on (031). The names say what the choice is
    // for, capability against cost, rather than naming a vendor's model, which
    // changes under the same tier on every release.
    modelTier: 'Runs on',
    modelTierHint: 'Where the next session you start runs, and how much effort it spends. Quick is cheapest; Sustained runs the strongest models.',
    runtimePickLabel: 'Runs on {choice}. Change where the next session runs',
    runtimePickTitle: 'Where the next session runs',
    runsOn: 'Runs on',
    effort: 'Effort',
    effortQuick: 'Quick',
    effortThorough: 'Thorough',
    effortSustained: 'Sustained',
    effortUnavailable: 'Not available to you right now',
    specificModel: 'A specific model',
    searchModels: 'Search {count} models',
    noModelMatch: 'No model matches',
    paidByPerson: 'Charged to your own Latere wallet, not to this project’s credits.',
    paidByProject: 'Charged to this project’s credits.',
    // The price of a tier, per million tokens in and out, the unit the rate
    // card is kept in, so what is shown is what the run is billed from.
    tierPrice: '{in} / {out} per Mtok',
    errBadTier: 'That model tier isn’t offered. Pick one from the list.',
    startFailed: 'Couldn’t start this session. Try again in a moment.',
    errPaperGone: 'That paper is no longer on this project.',
    startNeedsCredits: 'This project needs {reserve} available to start a session. Allocate credits to it from your wallet.',
    removePaperFailed: 'Couldn’t remove that paper. Try again in a moment.',
    errPaperHasSessions: 'This paper has sessions, so it cannot be removed from the project.',
    errRemoveNotLead: 'Only a project lead can remove a paper.',
    // Moving a paper to another project (035), with its replications.
    movePaper: 'Move to another project',
    movePaperTitle: 'Move this paper',
    movePaperBody: 'The paper and all its replication sessions move to the project you choose. What each session spent stays with the project that paid for it.',
    movePaperTo: 'Move to',
    movePaperConfirm: 'Move paper',
    movingPaper: 'Moving…',
    movePaperNowhere: 'You have no other project to move this into. Create one first.',
    movePaperFailed: 'Couldn’t move that paper. Try again in a moment.',
    errPaperRunning: 'A session for this paper is running. Wait for it to finish, or stop it, then move the paper.',
    errPaperAlreadyThere: 'That project already has this paper.',
    errMovePublic: 'A published project’s papers can’t be moved. Withdraw the publication first.',
    errMoveTrashed: 'A project in the trash can’t be changed. Restore it first.',
    errMovePaperNotLead: 'You need to lead both projects to move a paper between them.',
    // What a project is about (038).
    describeAdd: 'Add a description',
    describeEdit: 'Edit the description',
    describePlaceholder: 'What is this project about? One paragraph is enough.',
    describeFailed: 'Couldn’t save that. Try again in a moment.',
    draftIt: 'Write a draft',
    draftModelHint: 'Which model writes project descriptions when somebody asks for a draft. The model id must also appear in the price list below, or drafting is refused because it cannot be priced.',
    draftUsesAgentKey: 'Uses the Anthropic key this deployment already uses for sessions. Nothing else to set.',
    draftNeedsOwnKey: 'Needs its own key in this deployment’s environment (DRAFT_API_KEY), because the platform holds no other credential for this vendor.',
    draftProvider: 'Drafting model',
    draftProviderNone: 'None: descriptions are written by hand',
    draftProviderOpenAI: 'Another endpoint (OpenAI-compatible)',
    draftBaseURL: 'Endpoint',
    draftBaseURLPlaceholder: 'https://api.openai.com/v1',
    draftModel: 'Model id',
    draftModelPlaceholder: 'claude-haiku-4-5-20251001',
    drafting: 'Writing…',
    draftHint: 'Writes a first draft from this project’s papers, using a small amount of its credits. Nothing is saved until you save it.',
    draftFailed: 'Couldn’t write a draft just now. Try again, or write it yourself.',
    // A paper's own bibliography (037): what it is, not just what it is called.
    editPaper: 'Correct these details',
    editPaperTitle: 'Correct this paper',
    editPaperBody: 'These details come from the paper’s published record. Anything you change here is kept. A later lookup fills in only fields that are still blank.',
    editPaperFailed: 'Couldn’t save those details. Try again in a moment.',
    editPaperBadYear: 'That doesn’t look like a year.',
    paperFieldTitle: 'Title',
    paperFieldAuthors: 'Authors',
    paperFieldAuthorsHint: 'One per line, in the order they appear on the paper.',
    paperFieldVenue: 'Journal or conference',
    paperFieldYear: 'Year',
    paperFieldVolume: 'Volume',
    paperFieldIssue: 'Issue',
    paperFieldPages: 'Pages',
    paperFieldPublisher: 'Publisher',
    paperFieldAbstract: 'Abstract',
    paperFieldAbstractHint: 'As published by the paper’s source.',
    showMore: 'more',
    showLess: 'less',
    paperNoDetails: 'No details yet',
    etAl: 'et al.',
    fillIn: 'Fill in details',
    fillingIn: 'Looking up…',
    fillInDone: 'Filled in {n} paper(s).',
    fillInNothing: 'Nothing more to fill in.',
    fillInFailed: 'Couldn’t look those up. Try again in a moment.',
    paperSessions: '{n} session(s)',
    paperNeverRun: 'not started',
    paperUntitled: 'Looking up the title…',
    errNotAllowed: 'Your account cannot do this right now. Sign out and back in. If that does not help, ask a platform admin.',
    errOffline: 'You appear to be offline. Check your connection and try again.',
    rename: 'Rename',
    renameFailed: 'Couldn’t rename this project. Try again in a moment.',
    // The two steppers on every number field, as a screen reader says them.
    stepDown: 'Decrease',
    stepUp: 'Increase',
    // Ages and durations, as when.ts says them. An age rounds down to one unit
    // and stops at a week; a duration keeps the minutes, and the seconds where
    // they are wanted.
    justNow: 'just now',
    agoMinute: '1 minute ago',
    agoMinutes: '{n} minutes ago',
    agoHour: '1 hour ago',
    agoHours: '{n} hours ago',
    agoDay: '1 day ago',
    agoDays: '{n} days ago',
    durSeconds: '{s}s',
    durMinutes: '{m}m',
    durMinutesSeconds: '{m}m {s}s',
    durHoursMinutes: '{h}h {m}m',
    save: 'Save',
    cancel: 'Cancel',
    // project detail
    projectSessions: 'Sessions',

    runningN: '{n} running',
    stop: 'Stop',
    stopping: 'Stopping…',
    stopped: 'stopped',
    stopError: 'Couldn’t stop this session. It is still running. Try again.',
    // A session that failed used to print the worker's own error string here,
    // such as "put replication-manifest.json in latere/x: unexpected status
    // 422". These
    // say what class of thing went wrong and who can move it; the original text
    // is still one hover away, for whoever is actually debugging it.
    failUnresolvable: 'Stopped: the paper could not be identified from its link.',
    failSource: 'Stopped: the paper’s source could not be reached.',
    failSecret: 'Stopped: a file in the results looked like it held a password or key.',
    failPublish: 'Stopped while saving the results to GitHub.',
    failCredits: 'Stopped: this project ran out of credits.',
    failGeneric: 'This session didn’t finish.',
    failDetail: 'What the run reported: {cause}',
    verdict: 'Verdict',
    artifacts: 'Artifacts',
    noVerdict: 'No finished session yet. The verdict appears once a session finishes.',
    // A run whose paper is no longer on the project: a paper that has never been
    // run can be removed, and the run it once had stays.
    otherSessions: 'Sessions of papers no longer on this project',
    verdictUnread: 'Verdict unavailable',
    noArtifacts: 'No artifacts committed yet.',
    selectArtifact: 'Select a file to view it.',
    artifactError: 'Couldn’t open this file. Try again in a moment.',
    graded: 'Graded',
    // Two different judgments, and conflating them was the defect: the run's
    // grade says whether the audit met the method's bar, the paper's verdict
    // says whether it reproduced. A careful audit of a paper that does not
    // reproduce passes the first and fails the second.
    runRecord: 'The run',
    methodCheck: 'Method check',
    noVerdictRecorded: 'No verdict recorded',
    latestSessionInner: 'Latest session · inner loop',
    ofComponents: 'of {n} components',
    openVerdict: 'Open the verdict',
    statSpend30d: 'Spend · last 30 days',
    runningForTitle: 'How long this session has been running. It is charged for the whole time.',
    // The findings (021b). The rate never appears without the decomposition it
    // summarizes, because the rubric is explicit that the per-component table is
    // the result and the rate is a derived footnote to it.
    boundary: 'Inner / outer boundary',
    claim: 'Claim',
    loop: 'Loop',
    outcome: 'Outcome',
    evidenceOrBlocker: 'Evidence / blocker',
    loopInner: 'Inner',
    loopOuter: 'Outer',
    outVerified: 'Verified',
    outPartial: 'Partial',
    outBlocked: 'Blocked',
    outNotScored: 'Not scored',
    unresolved: 'Mismatches and unresolved claims',
    unresolvedNone: 'Every inner-loop claim reproduced, and none of them disagreed with the paper.',
    unresolvedHint: 'Claims that did not fully reproduce, and claims that reproduced but disagreed with the paper.',
    reportedIs: 'paper',
    observedIs: 'run',
    mismatchNumeric: 'Number differs',
    mismatchOrdering: 'Ordering differs',
    mismatchContradiction: 'The paper disagrees with itself',
    mismatchTypo: 'An error in the paper',
    decisions: 'Decisions the paper does not state',
    decisionAssumed: 'The run assumed',
    decisionWhere: 'Not stated at',
    decisionSensitivity: 'If that is wrong',
    componentsOf: '{verified} verified · {partial} partial · {blocked} blocked, of {total}',
    innerLoopRate: 'Inner loop',
    // Two different absences, kept apart: a run that never wrote the file, and
    // one whose file the system refused. Collapsing them would tell a reader the
    // paper had no boundary when what happened is that the run misreported it.
    noFindings: 'This session did not record its findings as data, so only its verdict and rate are shown. This is usual for runs from before that file existed.',
    findingsInvalid: 'This session’s findings file was rejected by the system, so only its verdict and rate are shown.',
    releasedTitle: 'What the authors released',
    releasedHint: 'Every place the run looked for the paper’s artifacts, and what it found.',
    scorecard: 'Open-science scorecard',
    searchedIn: 'Searched',
    resFound: 'Found',
    resPartial: 'Partial',
    resNone: 'None',
    resDead: 'Dead link',
    resNotFetched: 'Not fetched',
    critCode: 'Code',
    critData: 'Data',
    critLicense: 'License',
    critPreregistration: 'Preregistration',
    critSupplement: 'Supplementary artifacts',
    redistribution: 'Redistribution',
    methodDigest: 'Method digest',
    repository: 'Repository',
    viewTranscript: 'Transcript',
    publishedNote: 'Public. Signed off {when}.',
    // members
    members: 'Collaborators',
    noMembers: 'No collaborators yet.',
    inviteEmail: 'colleague@university.edu',
    invite: 'Invite',
    inviteHint: 'Invite by email. They get a message with a link and can sign in straight away. Sessions they start here are paid for from this project’s credits.',
    remove: 'Remove',
    pending: 'pending',
    inviteError: 'Couldn’t add that person. Check the email address and try again.',
    removeMemberFailed: 'Couldn’t remove that person. They are still a collaborator; try again in a moment.',
    // researcher profile (006n)
    profileTitle: 'Researcher profile',
    profileIntro:
      'The name your published sign-offs are made under. It is not verified: the platform records what you enter and ties it to your account. A published project reports these details as they were when you signed it.',
    profileName: 'Display name',
    profileNamePlaceholder: 'Ada Lovelace',
    profileInstitution: 'Institution',
    profileInstitutionPlaceholder: 'Optional: university, lab or company',
    profileContact: 'Public contact email',
    profileContactPlaceholder: 'Published with anything you sign off',
    profileOrcid: 'ORCID',
    profileScholar: 'Google Scholar',
    profileScholarPlaceholder: 'https://scholar.google.com/citations?user=…',
    saving: 'Saving…',
    profileSaved: 'Saved.',
    profileSaveError: 'Couldn’t save your profile. Try again in a moment.',
    errInvalidContact: 'That is not a valid email address.',
    errInvalidOrcid: 'An ORCID iD looks like 0000-0002-1825-0097.',
    errInvalidScholar: 'That is not a Google Scholar profile URL.',
    // publish
    publishTitle: 'Publish · sign-off',
    publishBody:
      'Publishing makes this project, with its sessions, transcripts and artifacts, visible to everyone on the platform. Only a project that passed the redistribution check can be published. You can withdraw it later, but copies made while it was public remain.',
    publishAffirm: 'I take responsibility for publishing these reproduction results.',
    flipRepoLabel: 'Also make the GitHub repository public',
    publishConfirm: 'Sign off & make public',
    publishNotClear: 'This project cannot be made public yet: a session committed content that cannot be redistributed.',
    publishNothingPublished: 'There is nothing to publish yet: no session in this project has committed a reproduction.',
    publishUnfinished: 'This project cannot be made public yet: a session started publishing and never finished.',
    publishUnreadable: 'We couldn’t read the record of a session in this project, so publishing was not attempted. Try again in a moment.',
    publishBlocked: 'This project cannot be made public yet.',
    publishBlockersTitle: 'What blocks publishing',
    publishOpenSession: 'Open session',
    blockerNotClear: 'Third-party data was excluded from this reproduction, so it cannot be redistributed.',
    blockerSecret: 'A credential was found in this session’s transcript, so it cannot be redistributed.',
    blockerUnfinished: 'This session began publishing and never finished, so it has no reproduction record.',
    blockerUnreadable: 'This session’s reproduction record could not be read just now.',
    blocksPublishing: 'Blocks publishing',
    publishError: 'Couldn’t make the project public. Try again in a moment.',
    attestationNote: 'Attestation v2 · recorded against your account',
    // publish wizard (006n)
    publishStart: 'Make this project public…',
    publishStepOf: 'Step {n} of 3',
    publishStep1: 'What becomes public',
    publishStep2: 'Who signs it',
    publishStep2Body:
      'Recorded with the project as shown here. Editing your profile later will not change what this project reports.',
    publishStep3: 'Responsibility',
    publishAttestation:
      'I confirm that I am responsible for these reproduction results, that I have reviewed what is about to be published, and that the contact address above may be published so anyone can ask about them.',
    publishContactRequired: 'A contact email is required, so that readers of a published result can ask about it.',
    publishSaveToProfile: 'Also save these details to my profile',
    continueStep: 'Continue',
    backStep: 'Back',
    // the signed-off card (006n)
    signedOffBy: 'Signed off by',
    // "Signer" read as the person, which the line above already names, so next
    // to a contact address it looked like the same field twice. It is neither:
    // it is the account the sign-off was made from (006n).
    signedOffSigner: 'Signed in as',
    signedOffContact: 'Contact',
    signedOffOrcid: 'ORCID',
    signedOffScholar: 'Scholar',
    // the public profile (006r)
    profileHandle: 'Public handle',
    profileHandlePlaceholder: 'ada-lovelace',
    profilePublicLabel: 'Give me a public profile page',
    profilePublicHint:
      'Publishes the fields above at /u/{your handle}, together with the public reproductions you have signed. Every one of these fields already appears on each sign-off; the page adds the list. A public profile is required before you can publish a project. You can turn it off at any time: published attestations keep what they recorded and stop linking to your page.',
    publishPublicProfile: 'Publish my researcher profile at /u/{handle}',
    publishProfileRequired:
      'Publishing puts your name on this result, so the name must link to a public page. Choose a handle above and turn on the public profile.',
    profileNotFound: 'No such profile',
    profileNotFoundBody: 'This researcher has no public page, or the address is wrong.',
    profileBackHome: 'Go to ReplicHAI',
    profileSignedWork: 'Signed reproductions',
    profileNoSignedWork: 'Nothing published under this name yet.',
    profileUnverified:
      'These details are self-reported and unverified: ReplicHAI does not check that somebody works where they say. It does guarantee that the work listed below is public and was signed by this account.',
    errHandleTaken: 'That handle is already taken.',
    errHandleReserved: 'That handle is reserved.',
    errInvalidHandle: 'Use 3\u201339 characters: a\u2013z, 0\u20139 and hyphens.',
    // withdrawing a publication (006o)
    withdrawStart: 'Withdraw this publication…',
    withdrawTitle: 'Withdraw this publication',
    withdrawBody:
      'The project becomes private again. It is removed from the registry, and its sessions, transcripts and artifacts are visible only to you and your collaborators. The GitHub repository is made private again. Your sign-off stays on record, and you can publish again at any time.',
    withdrawNotARecall:
      'This stops new access. It does not remove copies already taken: forks, clones and caches made while the project was public remain.',
    withdrawReason: 'Why are you withdrawing it? (optional, for your own record)',
    withdrawReasonHint: 'e.g. an unreleased figure was included in the artifacts',
    withdrawConfirm: 'Withdraw & make private',
    withdrawBusy: 'Withdrawing…',
    withdrawError: 'Couldn’t make the project private again. Try again in a moment.',
    // transcript
    transcript: 'Transcript',
    transcriptNotReady: 'The transcript will appear once this session has published.',
    transcriptError: 'Couldn’t load the transcript. Try again in a moment.',
    events: 'Events',
    eventsCounted: '{n} events',
    showEvents: 'Show events',
    toolsUsed: 'Tools the agent used',
    hideLifecycle: 'Hide lifecycle events',
    search: 'Search',
    close: 'Close',
    searchTranscript: 'Search this transcript',
    searchNoMatch: 'Nothing in this transcript matches.',
    // The scrubber says which question its bars answer, because "where the
    // money went" and "where the chatter was" are different shapes (006s).
    scrubLabel: 'Jump to a point in the run',
    // "New tokens" rather than "tokens": on a real session the cache reads are
    // 99% of the total and grow with the conversation, so ranking steps by the
    // total would measure length, not work (006s).
    scrubByTokens: 'Bars and event figures are new tokens: input + output, cache reads excluded. Open an event for its full breakdown.',
    scrubByEvents: 'Bar height is the number of events recorded. No tokens were charged in this run.',
    // The sentence is the card's body; the label is what fits a filter row.
    thinking: 'thinking (content not provided by the backend)',
    messageLabel: 'message',
    thinkingLabel: 'thinking',
    toolCall: 'tool call',
    toolResult: 'tool result',
    grade: 'Grade',
    noContent: '(no content)',
    usage: 'Token usage',
    usageInput: 'Input',
    usageOutput: 'Output',
    usageCached: 'Cached',
    usageCacheWritten: 'Cache written',
    usageTotal: 'Total',
    usageNotRecorded: 'Token usage was not recorded for this session.',
    // timeline + live stream
    timeline: 'Timeline',
    inProgress: 'in progress',
    liveStreaming: 'live',
    transcriptWaitingLive: 'Waiting for the agent’s first events…',
    // tiers + phases

    tierUser: 'User',
    tierAdmin: 'Admin',
    tierNone: 'No access',
    'phase.submitted': 'Submitted',
    'phase.resolving': 'Resolving',
    'phase.running': 'Running',
    'phase.publishing': 'Publishing',
    'phase.done': 'Done',
    'phase.failed': 'Failed',
}

// TKey is every translation key, derived from the English block so a new locale
// is checked to cover them all.
export type TKey = keyof typeof en

// strings holds every locale. Adding one, such as `zh: { … }`, is where
// TypeScript enforces completeness against TKey.
const strings: Record<Lang, Record<TKey, string>> = { en }

// readLang accepts what localStorage held. Anything that names no locale,
// whether an older build's value, a hand edit, or the absent value of a first
// visit, reads as English rather than reaching the string table as a block
// that is not there, where the first t() used to throw.
export function readLang(raw: string | null): Lang {
  return raw !== null && raw in strings ? (raw as Lang) : 'en'
}

// T is the translate function's shape: a key, and the values for its {name}
// placeholders.
export type T = (key: TKey, vars?: Record<string, string>) => string

// translator is t and raw for one language, with no hook around them, for the
// modules that format what the screens show, such as when.ts and transcript.ts,
// which have no component to read a hook in, and for the tests that pin their
// output. useI18n is this, remembered for the language the reader chose.
export function translator(lang: Lang): { t: T; raw: T } {
  // raw is the string as it is written, seams and all. Only Seams (ui.tsx) has
  // any use for it; everything else wants the sentence.
  const raw: T = (key, vars) => {
    // Typed against TKey: a key with no string is a compile error rather than
    // a landing band that paints `landingVerdictBody` at a reader. The runtime
    // fallback below is for a locale that is missing a key English has, not
    // for a key that exists nowhere.
    const table = strings[lang] as Record<string, string>
    let s = table[key] ?? (strings.en as Record<string, string>)[key] ?? key
    if (vars) for (const k of Object.keys(vars)) s = s.replace(`{${k}}`, vars[k])
    return s
  }
  // t is the sentence with seams removed. A seam is an instruction to whoever
  // sets the line, and every other consumer, such as a tab title, an aria-label
  // or a share card, wants the words. Stripping here rather than at each call
  // site makes that the default; the tab once read "No page|at this address".
  // The seam stands in place of the space it may break at, so removing it puts
  // the space back. Deleting it outright once produced "No pageat this address".
  const t: T = (key, vars) => raw(key, vars).replace(/\s*\|\s*/g, ' ')
  return { t, raw }
}

// useI18n stores the chosen language in localStorage and returns a translate
// function that interpolates {name} placeholders.
export function useI18n() {
  const [lang, setLangState] = useState<Lang>(() => readLang(localStorage.getItem('lang')))
  const setLang = useCallback((l: Lang) => {
    localStorage.setItem('lang', l)
    setLangState(l)
    document.documentElement.lang = l
  }, [])
  const { t, raw } = useMemo(() => translator(lang), [lang])
  return { lang, setLang, t, raw }
}

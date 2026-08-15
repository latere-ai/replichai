import { useCallback, useState } from 'react'

// English only for now. To add a language: add its code to the Lang union and a
// matching block to `strings`. Because a translation key is derived from the
// English block below (TKey), TypeScript then *requires* the new block to
// translate every string — a locale cannot ship half-done — and a language
// switcher becomes a two-line change.
export type Lang = 'en'

// I18n is what a component needs to render text: the chosen language, a setter,
// and the translate function. It lives here rather than in App so that the
// error-copy module can type against it without importing the whole app.
export type I18n = ReturnType<typeof useI18n>

// en is the source of truth: every key the product uses, in English. A new
// locale is typed against its keys, so it cannot omit one.
const en = {
    // auth
    // The landing (017). The claim is a question because that is the question
    // the product answers, and the lead says what it does that nothing else in
    // the field does: it runs the paper. The three points are the inner/outer
    // boundary — the thing worth putting in front of somebody before they have
    // an account — and the card promises only what 006q made true, that signing
    // in is open and a session is what spends.
    landingClaim: 'Does the paper actually reproduce?',
    landingLead:
      'Give it a paper. replichai finds what the authors released, implements and re-runs the part a machine can run, records every decision the paper left unwritten, and returns a verdict you can take apart.',
    landingInnerTitle: 'The inner loop',
    landingInnerBody:
      'The algorithm, model, or analysis pipeline. Implemented, run, and published as its own repository with the agent’s own commit history.',
    landingOuterTitle: 'The outer loop',
    landingOuterBody:
      'The study that validates it with people. Named, bounded, and never simulated — pretending otherwise is the most common way reproducibility claims go wrong.',
    landingVerdictTitle: 'The verdict',
    landingVerdictBody:
      'Authored by the system, signed by a named researcher, and published with the full transcript, so you can check the reasoning instead of taking the number.',
    // The landing as a place (029): the section headings, the figure captions,
    // and the close. The claim, the lead, and the three points above are 017's
    // and are reused verbatim — the argument did not change, only how much room
    // it now has.
    lpLoopsTitle: 'One loop closes. One never will.',
    lpLoopsCaption:
      'The algorithm re-runs, so it is drawn closed. The study with people does not, so it is drawn open — and nothing here pretends otherwise.',
    lpPipelineTitle: 'What a session actually does',
    lpPipelineCaption:
      'Four steps, each leaving something you can open afterwards: the release it found, the repository it wrote, the transcript of how it got there.',
    lpStepPaper: 'The paper',
    lpStepPaperBody: 'Resolved to one identity, so two people auditing the same work are auditing the same thing.',
    lpStepPaperArtifact: 'A paper key',
    lpStepRelease: 'What the authors released',
    lpStepReleaseBody: 'Code, data, licences, preregistration — found and recorded, including what is missing.',
    lpStepReleaseArtifact: 'A release record',
    lpStepRerun: 'The re-run',
    lpStepRerunBody: 'The machine-runnable half implemented and executed, with every decision the paper left unwritten written down.',
    lpStepRerunArtifact: 'A repository, committed live',
    lpStepVerdict: 'The verdict',
    lpStepVerdictBody: 'What reproduced, what did not, and what could not be judged — component by component.',
    lpStepVerdictArtifact: 'A manifest and a transcript',
    lpAnatomyTitle: 'A verdict you can take apart',
    lpAnatomyCaption:
      'Three things, kept separate on purpose. Fuse them and you get a number to trust; keep them apart and you get a result to check.',
    lpPartClaim: 'The claim',
    lpPartClaimBody: 'What the agent says it reproduced, and how.',
    lpPartClaimBy: 'the agent',
    lpPartEvidence: 'The evidence',
    lpPartEvidenceBody: 'The full session transcript, committed beside the work rather than summarised away.',
    lpPartEvidenceBy: 'the record',
    lpPartVerdict: 'The verdict',
    lpPartVerdictBody: 'Authored over both by the system, and signed by a named researcher who stands behind it.',
    lpPartVerdictBy: 'the system, signed',
    lpStripTitle: 'Published reproductions',
    lpStripCaption: 'Signed, opened, and readable without an account. Open one before you decide whether this is for you.',
    lpCloseTitle: 'Read one before you sign up.',
    lpCloseBody: 'Everything published here is open — the verdict, the repository, and the transcript that produced them.',
    signInTitle: 'Sign in',
    signInBody: 'Signing in is open and costs nothing. A session is what spends, and only when you start one.',
    signInNote: 'Built for research with human participants.',
    signInWith: 'Sign in with latere',
    authUnavailableTitle: 'ReplicHAI is unavailable',
    authUnavailableBody: 'We couldn’t check your session. You may be offline, or the service may be temporarily unavailable.',
    retry: 'Try again',
    // Sign-in is open (006q), so this screen is no longer "wait for an admin".
    // It is not only "you were banned" either: a session minted before the
    // change, an address the identity provider did not return, and an address
    // already bound to another account all land here too. Naming a ban would
    // accuse somebody of something that did not happen, so the copy says what
    // is certain — no access right now — and offers the two things that help.
    noAccessTitle: 'No access to this account',
    noAccessBody:
      "You're signed in as {email}, but this account cannot use replichai right now. Signing out and back in fixes most cases; if it persists, a platform admin has revoked access and can restore it.",
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
      'Everyone who has signed in, at what tier, and what their wallet holds. Signing in is open: an account appears here on its first login, with no credits until you give it some. Grants and credits can both be seeded by email before anyone signs in, and bind when they do. Revoking bans an account — it stays listed so you can undo it.',
    platformPolicy: 'Platform policy',
    savePolicy: 'Save policy',
    startReserve: 'Start reserve (USD)',
    overdraftCap: 'Overdraft floor (USD)',
    overdraftHint:
      'How far below zero one project may go before a run is stopped. A session runs to completion once started \u2014 killing one mid-run turns spend into nothing \u2014 so this bounds a runaway rather than metering normal work; the start reserve is what stops the next session. It is not shown to project members: a floor somebody knows is one they plan around.',
    rateCardHint: 'What a session is charged, per million tokens. A model the list does not name is charged the dearest rates on it.',
    rateModel: 'Model',
    rateInput: 'Input',
    rateOutput: 'Output',
    rateCacheRead: 'Cache read',
    rateCacheWrite: 'Cache write 5m / 1h',
    startReserveHint:
      'How much a project must have available before a session may start in it. It is held while the session runs and returned when it settles, so it bounds how many runs one project can have in flight at once \u2014 not what a run finally costs, which is metered from the tokens it actually burned.',
    policyUpdatedBy: 'Last changed by {who}',
    people: 'People',
    noGrants: 'Nobody has signed in yet.',
    grant: 'Grant',
    revoke: 'Revoke',
    grantHint: 'Anyone may sign in and look around; what they cannot do without credits is run a session. Grant to set someone\u2019s tier, revoke to ban them. Admins can also set platform policy.',
    topup: 'Add credits',
    topupPrompt: 'Credits to add to {email}\u2019s wallet, in US dollars. They allocate what they need to a project from there.',
    walletOf: 'What {email} has left to allocate to a project.',
    adminError: 'That change didn’t go through. Try again in a moment.',
    adminNotAdmin: 'Only a platform admin may change this.',
    // credits (011)
    walletTitle: 'Your credits. A project needs {reserve} available before a session can start there \u2014 allocate from here to fund one.',
    // Nothing to spend and nothing ever spent: a new account, not one that has
    // run down. Showing \u201c$0.00\u201d to somebody who never had any reads as a
    // loss rather than a starting state.
    walletEmpty: 'no credits yet',
    walletEmptyTitle: 'This account has no credits yet. Ask a platform admin to add some, or work in a project somebody else has funded.',
    errNoCredits: 'This project is out of credits \u2014 allocate more to it to keep going.',
    errNoCreditsAtAll: 'No credits yet \u2014 ask a platform admin to add some, or work in a project somebody else has funded.',
    // buying credits (022e)
    beta: 'BETA',
    navWallet: 'Wallet',
    walletHeading: 'Your wallet',
    walletDesc: 'Credits you buy and spend. A session draws on the project you allocate to, not on the wallet directly.',
    topUp: 'Top up',
    topUpWallet: 'Top up wallet',
    topUpTitle: 'Top up your wallet',
    topUpChoose: 'Choose an amount to buy — you can always top up again later.',
    topUpOther: 'Other',
    topUpMinHint: 'US dollars, minimum {min}.',
    topUpSecure: 'You’ll pay securely on Stripe’s checkout page; EU customers are billed in euros. Credits land in your wallet the moment the payment clears.',
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
    topUpFeeWhy: 'Covers card processing and platform costs. The rest becomes credits at their raw compute price.',
    topUpGet: 'Credits added',
    topUpMin: 'The minimum top-up is {min}.',
    topUpProcessing: 'Payment received \u2014 your credits will appear here shortly.',
    errPaymentsOff: 'Card payments aren\u2019t set up on this deployment yet.',
    errBelowMin: 'That\u2019s below the minimum top-up.',
    checkoutFailed: 'Couldn\u2019t start the payment. Try again in a moment.',
    // the frozen account (022d/022f)
    frozenBanner: 'A refund or chargeback left your balance negative, so your account is view-only. Top up to lift the hold.',
    frozenControl: 'Your account is on hold \u2014 top up to lift it.',
    errFrozen: 'Your account is on hold after a refund left the balance negative. Top up to lift it.',
    frozen: 'Frozen',
    unfreeze: 'Unfreeze',
    unfreezeTitle: 'Let this account work again despite its debt. The waiver clears itself once the balance is settled.',
    refreeze: 'Re-freeze',
    refreezeTitle: 'This account was waived while still negative. Re-freeze to reinstate the hold.',
    onlyOwing: 'Only owing',
    noOwing: 'No accounts are in the red.',
    solvency: 'Outstanding liability',
    solvencyTitle: 'Credits sold and not yet delivered as compute: every wallet and pot summed. Watch it against what the vendor account holds.',
    // a project's own credits, and the two moves on them
    projectCredits: 'Credits',
    creditsBalance: 'Balance',
    creditsAvailable: 'Available now',
    creditsAvailableTitle:
      'What is still free to commit. It is lower than the balance while sessions are running \u2014 each holds the start reserve until it settles, which is why a project with credits can still refuse a new session.',
    creditsEnough: 'Enough to start a session. {reserve} is held while one runs, and what it does not spend comes back.',
    creditsShort: 'Not enough to start a session \u2014 this project needs {reserve} available. Allocate more from your wallet.',
    creditsAmount: 'Amount in US dollars',
    creditsAllocate: 'Allocate',
    creditsReclaim: 'Take back',
    errAllocateShort: 'Your wallet doesn’t have that much to allocate. Top up to add more.',
    errReclaimHeld: 'That much isn’t free to take back yet — credits held by a running session come back when it settles.',
    errMoveNotLead: 'Only a project lead can take credits back out.',
    errMoveNotMember: 'You are not a member of this project.',
    errMoveAmount: 'Enter an amount above zero.',
    errMoveFailed: 'That transfer didn’t go through. Try again in a moment.',
    sessionCostTitle: 'What this session cost its project.',
    sessionCostRunningTitle: 'What this session has cost so far. It is charged when the session ends.',
    signOut: 'Sign out',
    back: 'Back',
    // appearance: three choices, not a toggle — "auto" follows the operating
    // system, and the other two pin it regardless of what the machine does.
    theme: 'Appearance',
    themeAuto: 'Auto',
    themeAutoHint: 'Auto · follow the system',
    themeDark: 'Dark',
    themeLight: 'Light',
    // projects
    projectsIntro:
      'A project holds a paper and every replication session run against it. Each session is a durable, inspectable run — its trajectory and token usage are yours to check, never a score to rank papers by.',
    statProjects: 'Projects',
    statSessionsRun: 'Replication sessions',
    statWallet: 'Credits',
    statementShow: 'Statement',
    statementHide: 'Hide',
    statementEmpty: 'Nothing yet \u2014 a platform admin adds credits to your account.',
    ledgerTopup: 'Added by an admin',
    ledgerAllocate: 'Allocated to a project',
    ledgerReclaim: 'Taken back from a project',
    ledgerOther: 'Adjustment',
    // The named forms (024b): a prefix the project name follows, linked when the
    // project still exists and plain text once it is purged.
    ledgerAllocateTo: 'Allocated to',
    ledgerReclaimFrom: 'Taken back from',
    // The merged list (018). Ownership is a column, not a place to look, so the
    // owner of your own project is "You" — the common row, and the one nobody
    // should have to read a name to recognise.
    colPaper: 'Paper',
    colOwner: 'Owner',
    colModified: 'Last modified',
    colStatus: 'Status',
    colVisibility: 'Visibility',
    ownerYou: 'You',
    ownerUnknown: '—',
    noProjects: 'No projects yet. Start a new audit to open one.',
    // The trash (019). Two steps, because the dangerous one has to be
    // deliberate: the row action is reversible and says so, and the words that
    // matter on the permanent one are what it does *not* remove — the
    // repositories close, they are not destroyed.
    download: 'Download everything',

    navTrash: 'Trash',
    trashIntro:
      'Projects you removed. They are out of your list and nobody else can reach them, and everything they hold is still here until you delete them for good.',
    noTrash: 'Nothing in the trash.',

    trashProject: 'Move to trash',
    trashTitle: 'Move this project to the trash?',
    trashBody:
      'It leaves your list and your collaborators’ lists. Its papers, sessions, and results stay exactly as they are, and you can restore it from the trash at any time.',
    trashPublicNote: 'This project is public. Moving it to the trash withdraws the publication and closes its repositories, and the retraction is recorded.',
    trashConfirm: 'Move to trash',
    trashing: 'Moving…',
    errSessionsRunning: 'A session is still running in this project. Stop it first.',
    trashFailed: 'Couldn’t move this project to the trash. Try again in a moment.',
    restore: 'Restore',
    restoring: 'Restoring…',
    restoreFailed: 'Couldn’t restore this project. Try again in a moment.',
    purge: 'Delete permanently',
    purgeTitle: 'Delete this project for good?',
    purgeBody:
      'The project, its papers, and every session it holds are removed from replichai and cannot be brought back.',
    purgeKeepsRepos:
      'The GitHub repositories its sessions published are closed, not destroyed. A reproduction the agent wrote is evidence; what this removes is replichai’s record of it.',
    purgeConfirm: 'Delete permanently',
    purging: 'Deleting…',
    errCreditsRemaining: 'This project still holds credits. Take them back first, then delete it.',
    purgeFailed: 'Couldn’t delete this project. Try again in a moment.',
    trashedBanner: 'This project is in the trash. Nothing can be changed or started here until you restore it.',
    sessionCount: '{n} session(s)',
    private: 'Private',
    public: 'Public',
    // public zone
    publicIntro:
      'Reproductions their initiators have signed off and opened to the platform. Each one carries its sessions and the full transcript that produced them, so you can check the reasoning rather than take the number.',
    noPublic: 'No public projects yet.',
    initiator: 'Published by',
    // What a reader with no account gets at an address that is not public (028).
    // The server answers one 404 for private, withdrawn, and never-existed, so
    // the copy claims none of them — it says what is certain and offers the two
    // things that help.
    notPublicTitle: 'Not public',
    notPublicBody: 'This address is not published. If you have been given access to it, sign in and you will land right back here.',
    notPublicBodyIn: 'This address is not published, or it is not shared with your account.',
    browseRegistry: 'Browse the registry',
    // submit / new project
    submit: 'Submit',
    submitting: 'Submitting…',
    activeSession: 'This paper is already being replicated in this project.',
    submitNoSpend: 'Nothing runs until the session starts.',
    newProjectDesc: 'Name it, add one or more papers, and invite collaborators. Each paper opens its own replication session.',
    projectNameLabel: 'Project name',
    projectNamePlaceholder: 'Optional — named from the first paper if left blank',
    papersLabel: 'Papers',
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
    resolveFailed: 'We couldn’t reach the site this paper is on. It may be down or busy — try again in a moment.',
    unresolvablePaper: 'We don’t recognise this link. Paste a DOI, an arXiv link, or the paper’s page on a publisher site.',
    noPapers: 'Add at least one paper.',
    // The short forms sit directly under the paper they belong to, so they only
    // have to say what about it — the row already says which one.
    errUnresolvable: 'We don’t recognise this link.',
    somePapersNotRecognised: 'The project was created. We didn’t recognise the links marked below — you can add those papers again from inside the project.',
    createFailed: 'Something went wrong on our side and the project wasn’t created. Try again in a moment.',
    // Working inside a project: adding a paper costs nothing, starting one is
    // the only act that spends.
    papers: 'Papers',
    projectNoPapers: 'No papers yet. Add one to say what this project is about.',
    addPaperHint: 'Paste a link. Adding it records what this project is about — nothing runs until you start it.',
    addPaperBtn: 'Add',
    addingPaper: 'Adding…',
    addPaperFailed: 'Couldn’t add that paper. Try again in a moment.',
    errPaperExists: 'This project already has that paper.',
    startSession: 'Start replication',
    startingSession: 'Starting…',
    // The model tier a session runs on (031). The names say what the choice is
    // *for* — capability against cost — rather than naming a vendor's model,
    // which changes under the same tier on every release.
    modelTier: 'Model',
    modelTierHint: 'Which model the next session you start runs on. A cheaper tier spends the project’s credits more slowly.',
    tierBest: 'Best',
    tierBalanced: 'Balanced',
    tierFast: 'Fast',
    // The price of a tier, per million tokens in and out — the unit the rate
    // card is kept in, so what is shown is what the run is billed from.
    tierPrice: '{in} / {out} per Mtok',
    errBadTier: 'That model tier isn’t offered. Pick one from the list.',
    startFailed: 'Couldn’t start this session. Try again in a moment.',
    errPaperGone: 'That paper is no longer on this project.',
    startNeedsCredits: 'This project needs {reserve} available to start a session. Allocate credits to it from your wallet.',
    removePaperFailed: 'Couldn’t remove that paper. Try again in a moment.',
    errPaperHasSessions: 'This paper has been run, so it stays on the project.',
    errRemoveNotLead: 'Only a project lead can remove a paper.',
    paperSessions: '{n} session(s)',
    paperNeverRun: 'not started',
    paperUntitled: 'Looking up the title…',
    errNotAllowed: 'Your account can’t do this right now. Signing out and back in fixes most cases; if it persists, ask a platform admin.',
    errOffline: 'You appear to be offline. Check your connection and try again.',
    rename: 'Rename',
    save: 'Save',
    cancel: 'Cancel',
    // project detail
    projectSessions: 'Sessions',

    runningN: '{n} running',
    stop: 'Stop',
    stopping: 'Stopping…',
    stopped: 'stopped',
    stopError: 'Couldn’t stop this session — it’s still running. Try again.',
    // A session that failed used to print the worker's own error string here —
    // "put replication-manifest.json in latere/x: unexpected status 422". These
    // say what class of thing went wrong and who can move it; the original text
    // is still one hover away, for whoever is actually debugging it.
    failUnresolvable: 'Stopped — the paper couldn’t be identified from its link.',
    failSource: 'Stopped — the paper’s source couldn’t be reached.',
    failSecret: 'Stopped — a file in the results looked like it held a password or key.',
    failPublish: 'Stopped while saving the results to GitHub.',
    failCredits: 'Stopped — this project ran out of credits.',
    failGeneric: 'This session didn’t finish.',
    failDetail: 'What the run reported: {cause}',
    verdict: 'Verdict',
    artifacts: 'Artifacts',
    noVerdict: 'No published session yet — the verdict appears once a session finishes.',
    // A run whose paper is no longer on the project: a paper that has never been
    // run can be removed, and the run it once had stays.
    otherSessions: 'Sessions of papers no longer on this project',
    verdictUnread: 'Verdict unavailable',
    noArtifacts: 'No artifacts committed yet.',
    selectArtifact: 'Select a file to view it.',
    artifactError: 'Couldn’t open this file. Try again in a moment.',
    graded: 'Graded',
    // Two different judgements, and conflating them was the defect: the run's
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
    runningForTitle: 'How long this session has been running. It is charged for every minute of it.',
    // The findings (021b). The rate never appears without the decomposition it
    // summarises, because the rubric is explicit that the per-component table is
    // the result and the rate is a derived footnote to it.
    boundary: 'Inner / outer boundary',
    claim: 'Claim',
    loop: 'Loop',
    outcome: 'Outcome',
    evidenceOrBlocker: 'Evidence / blocker',
    loopInner: 'INNER',
    loopOuter: 'OUTER',
    outVerified: 'VERIFIED',
    outPartial: 'PARTIAL',
    outBlocked: 'BLOCKED',
    outNotScored: 'NOT SCORED',
    unresolved: 'Mismatches & unresolved',
    unresolvedNone: 'Every inner-loop claim reproduced, and none of them disagreed with the paper.',
    unresolvedHint: 'What did not reproduce cleanly, and what disagreed with the paper even where it did.',
    reportedIs: 'paper',
    observedIs: 'run',
    mismatchNumeric: 'number differs',
    mismatchOrdering: 'ordering differs',
    mismatchContradiction: 'the paper disagrees with itself',
    mismatchTypo: 'an error in the paper',
    decisions: 'Decisions the paper left unwritten',
    decisionAssumed: 'The run assumed',
    decisionWhere: 'Left open at',
    decisionSensitivity: 'If that is wrong',
    componentsOf: '{verified} verified · {partial} partial · {blocked} blocked, of {total}',
    innerLoopRate: 'Inner loop',
    // Two different absences, kept apart: a run that never wrote the file, and
    // one whose file the system refused. Collapsing them would tell a reader the
    // paper had no boundary when what happened is that the run misreported it.
    noFindings: 'This session recorded no findings as data, so only its verdict and rate are shown. Runs from before that file existed are the usual reason.',
    findingsInvalid: 'This session wrote findings the system refused, so only its verdict and rate are shown.',
    releasedTitle: 'What the authors released',
    releasedHint: 'Every place the run looked for the paper\u2019s own artifacts, and what was there.',
    scorecard: 'Open-science scorecard',
    searchedIn: 'Searched',
    resFound: 'FOUND',
    resPartial: 'PARTIAL',
    resNone: 'NONE',
    resDead: 'DEAD',
    critCode: 'Code',
    critData: 'Data',
    critLicense: 'License',
    critPreregistration: 'Preregistration',
    critSupplement: 'Supplementary artifacts',
    redistribution: 'Redistribution',
    methodDigest: 'Method digest',
    repository: 'Repository',
    viewTranscript: 'Transcript',
    publishedNote: 'Public — signed off {when}.',
    // members
    members: 'Collaborators',
    noMembers: 'No collaborators yet.',
    inviteEmail: 'colleague@university.edu',
    invite: 'Invite',
    inviteHint: 'Invite by email. They get a message with a link, and can sign in straight away \u2014 sessions they start here are paid for out of this project\u2019s credits.',
    remove: 'Remove',
    pending: 'pending',
    inviteError: 'Couldn’t add that person. Check the email address and try again.',
    // researcher profile (006n)
    profileTitle: 'Researcher profile',
    profileIntro:
      'Who your published sign-offs are made under. It is not verified — the platform records the claim and binds it to your account — and it is only a starting point: what a published project reports is what you confirmed when you signed it.',
    profileName: 'Display name',
    profileNamePlaceholder: 'Ada Lovelace',
    profileInstitution: 'Institution',
    profileInstitutionPlaceholder: 'Optional — university, lab, or company',
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
      'Going public opens this project — its sessions, transcripts, and artifacts — to everyone on the platform. Only a project that passed the redistribution check can go public. You can withdraw it later, but copies made while it was public stay made.',
    publishAffirm: 'I take responsibility for publishing these reproduction results.',
    flipRepoLabel: 'Also make the GitHub repository public',
    publishConfirm: 'Sign off & make public',
    publishNotClear: 'This project cannot be made public: its reproduction did not pass the redistribution check.',
    publishError: 'Couldn’t make the project public. Try again in a moment.',
    attestationNote: 'Attestation v2 · recorded against your principal',
    // publish wizard (006n)
    publishStart: 'Make this project public…',
    publishStepOf: 'Step {n} of 3',
    publishStep1: 'What becomes public',
    publishStep2: 'Who stands behind it',
    publishStep2Body:
      'Recorded with the project exactly as it reads now. Editing your profile later will not change what this project reports.',
    publishStep3: 'Responsibility',
    publishAttestation:
      'I confirm that I am responsible for these reproduction results, that I have reviewed what is about to be published, and that the contact address above may be published so anyone can ask about them.',
    publishContactRequired: 'A contact email is required — a published result needs someone to ask.',
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
      'Publishes the fields above at /u/{your handle}, together with the public reproductions you have signed. Nothing here is new \u2014 every one of these fields already appears on any sign-off \u2014 what becomes visible is the list. Required before you can publish a project, and you can turn it off again at any time: published attestations keep everything they recorded, and simply stop linking to you.',
    publishPublicProfile: 'Publish my researcher profile at /u/{handle}',
    publishProfileRequired:
      'Publishing puts your name on this result, so the name has to lead somewhere. Choose a handle above and turn on the public profile.',
    profileNotFound: 'No such profile',
    profileNotFoundBody: 'This researcher has no public page, or the address is wrong.',
    profileBackHome: 'Go to replichai',
    profileSignedWork: 'Signed reproductions',
    profileNoSignedWork: 'Nothing published under this name yet.',
    profileUnverified:
      'Self-asserted and unverified: replichai does not check that somebody works where they say they work. What it does guarantee is that the work listed below is public and was signed by this account.',
    errHandleTaken: 'That handle is already taken.',
    errHandleReserved: 'That handle is reserved.',
    errInvalidHandle: 'Use 3\u201339 characters: a\u2013z, 0\u20139 and hyphens.',
    // withdrawing a publication (006o)
    withdrawStart: 'Withdraw this publication…',
    withdrawTitle: 'Withdraw this publication',
    withdrawBody:
      'The project returns to private: it leaves the public zone, and its sessions, transcripts, and artifacts close to everyone but you and your collaborators. The GitHub repository is made private again. Your sign-off is kept on record, and you can publish again at any time.',
    withdrawNotARecall:
      'This stops new access. It does not retrieve copies already taken — forks, clones, and caches made while the project was public stay where they are.',
    withdrawReason: 'Why are you withdrawing it? (optional, for your own record)',
    withdrawReasonHint: 'e.g. an unreleased figure slipped into the artifacts',
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
    scrubLabel: 'Seek to a moment in the run',
    // "New tokens" rather than "tokens": on a real session the cache reads are
    // 99% of the total and grow with the conversation, so ranking steps by the
    // total would measure length, not work (006s).
    scrubByTokens: 'Bars and event figures are new tokens: input + output, cache reads excluded. Open an event for its full breakdown.',
    scrubByEvents: 'Bar height is events recorded — nothing in this run was charged tokens.',
    // The sentence is the card's body; the label is what fits a filter row.
    thinking: 'thought for a moment (content not exposed by the backend)',
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
    transcriptWaitingLive: 'Waiting for the agent to emit its first events…',
    // tiers + phases

    tierUser: 'user',
    tierAdmin: 'admin',
    'phase.submitted': 'submitted',
    'phase.resolving': 'resolving',
    'phase.running': 'running',
    'phase.publishing': 'publishing',
    'phase.done': 'done',
    'phase.failed': 'failed',
}

// TKey is every translation key, derived from the English block so a new locale
// is checked to cover them all.
export type TKey = keyof typeof en

// strings holds every locale. Adding one — `zh: { … }` — is where TypeScript
// enforces completeness against TKey.
const strings: Record<Lang, Record<TKey, string>> = { en }

// useI18n stores the chosen language in localStorage and returns a translate
// function that interpolates {name} placeholders.
export function useI18n() {
  const [lang, setLangState] = useState<Lang>((localStorage.getItem('lang') as Lang) || 'en')
  const setLang = useCallback((l: Lang) => {
    localStorage.setItem('lang', l)
    setLangState(l)
    document.documentElement.lang = l
  }, [])
  const t = useCallback(
    (key: string, vars?: Record<string, string>) => {
      // Looked up by string, not TKey, so a dynamic key (a phase name built at
      // runtime) still resolves; a missing key falls back to English, then to
      // the key itself, so nothing renders blank.
      const table = strings[lang] as Record<string, string>
      let s = table[key] ?? (strings.en as Record<string, string>)[key] ?? key
      if (vars) for (const k of Object.keys(vars)) s = s.replace(`{${k}}`, vars[k])
      return s
    },
    [lang],
  )
  return { lang, setLang, t }
}

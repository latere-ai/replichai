import { useCallback, useState } from 'react'

// English only for now. The i18n scaffolding is kept so more languages can be
// added later once the product stabilises: add the code to the Lang union and a
// matching block to `strings`, and a language switcher becomes trivial again.
export type Lang = 'en'

// I18n is what a component needs to render text: the chosen language, a setter,
// and the translate function. It lives here rather than in App so that the
// error-copy module can type against it without importing the whole app.
export type I18n = ReturnType<typeof useI18n>

const strings: Record<Lang, Record<string, string>> = {
  en: {
    // auth
    tagline: 'Audit the reproducibility of a research paper.',
    signInWith: 'Sign in with latere',
    authUnavailableTitle: 'Replichai is unavailable',
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
    settings: 'Settings',
    access: 'Access',
    loading: 'Loading…',
    // platform admin (006k)
    platform: 'Platform',
    navAdmin: 'Admin',
    adminIntro:
      'Everyone who has signed in, at what tier, and what their wallet holds. Signing in is open: an account appears here on its first login, with no credits until you give it some. Grants and credits can both be seeded by email before anyone signs in, and bind when they do. Revoking bans an account — it stays listed so you can undo it.',
    platformPolicy: 'Platform policy',
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
    // a project's own credits, and the two moves on them
    projectCredits: 'Credits',
    creditsBalance: 'Balance',
    creditsAvailable: 'Available now',
    creditsAvailableTitle:
      'What is still free to commit. It is lower than the balance while sessions are running \u2014 each holds the start reserve until it settles, which is why a project with credits can still refuse a new session.',
    creditsEnough: 'Enough to start a session. {reserve} is held while one runs, and what it does not spend comes back.',
    creditsShort: 'Not enough to start a session \u2014 a project needs {reserve} available. Allocate more from your own credits.',
    creditsAmount: 'Amount in US dollars',
    creditsAllocate: 'Allocate',
    creditsReclaim: 'Take back',
    errMoveInsufficient: 'Not enough there to move. Credits held by a running session cannot be taken back until it settles.',
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
    myProjects: 'My projects',
    sharedWithMe: 'Shared with me',
    noProjects: 'No projects yet. Start a new audit to open one.',
    noShared: 'Nothing shared with you yet.',
    sessionCount: '{n} session(s)',
    private: 'Private',
    public: 'Public',
    // public zone
    publicIntro:
      'Reproductions their initiators have signed off and opened to the platform. Each one carries its sessions and the full transcript that produced them, so you can check the reasoning rather than take the number.',
    noPublic: 'No public projects yet.',
    initiator: 'Published by',
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
    startFailed: 'Couldn’t start this session. Try again in a moment.',
    errPaperGone: 'That paper is no longer on this project.',
    startNeedsCredits: 'This project needs {reserve} available to start a session. Allocate credits above.',
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
    noSessions: 'No sessions yet.',
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
    tierNone: 'none',
    tierUser: 'user',
    tierAdmin: 'admin',
    'phase.submitted': 'submitted',
    'phase.resolving': 'resolving',
    'phase.running': 'running',
    'phase.publishing': 'publishing',
    'phase.done': 'done',
    'phase.failed': 'failed',
  },
}

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
      let s = strings[lang][key] ?? strings.en[key] ?? key
      if (vars) for (const k of Object.keys(vars)) s = s.replace(`{${k}}`, vars[k])
      return s
    },
    [lang],
  )
  return { lang, setLang, t }
}

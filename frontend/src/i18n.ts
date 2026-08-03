import { useCallback, useState } from 'react'

// English only for now. The i18n scaffolding is kept so more languages can be
// added later once the product stabilises: add the code to the Lang union and a
// matching block to `strings`, and a language switcher becomes trivial again.
export type Lang = 'en'

const strings: Record<Lang, Record<string, string>> = {
  en: {
    // auth
    tagline: 'Audit the reproducibility of a research paper.',
    signInWith: 'Sign in with latere',
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
    settings: 'Settings',
    access: 'Access',
    loading: 'Loading…',
    // platform admin (006k)
    platform: 'Platform',
    navAdmin: 'Admin',
    adminIntro:
      'Everyone who has signed in, at what tier, and how much they may run. Signing in is open: an account appears here on its first login, with no budget until you give it some. Grants can still be seeded by email before anyone signs in, and bind when they do. Revoking bans an account — it stays listed so you can undo it.',
    platformPolicy: 'Platform policy',
    defaultQuota: 'Default session quota',
    defaultQuotaHint:
      'How many replication sessions somebody may run before you top them up. Zero \u2014 the default \u2014 means a new account arrives with nothing to spend, which is what keeps an open sign-in from spending the platform\u2019s budget. Completed and in-flight sessions count; a failed or stopped one is refunded. Platform admins are unlimited.',
    policyUpdatedBy: 'Last changed by {who}',
    people: 'People',
    noGrants: 'Nobody has signed in yet.',
    grant: 'Grant',
    revoke: 'Revoke',
    grantHint: 'Anyone may sign in and create projects; what they cannot do without budget is run a session. Grant to set someone\u2019s tier, revoke to ban them. Admins can also set platform policy.',
    topup: 'Top up',
    topupN: '+{n}',
    topupPrompt: 'Extra sessions for {email}, above the platform default (0 removes the top-up):',
    adminError: 'That change did not go through.',
    adminNotAdmin: 'Only a platform admin may change this.',
    used: 'Used',
    // session quota (006l)
    quotaLeft: '{n} sessions left',
    quotaTitle: '{used} of {limit} replication sessions used. Failed and stopped sessions are refunded.',
    quotaExhausted: 'You have no replication sessions left. Ask a platform admin for more.',
    // Nothing spent and nothing to spend: a new account, not an exhausted one.
    // Saying \u201c0 sessions left\u201d to somebody who never had any reads as a
    // fault rather than a starting state.
    quotaNone: 'no session budget',
    quotaNoneTitle:
      'This account has no session budget yet. Ask a platform admin to grant you some, or work in a project whose owner sponsors sessions.',
    errQuotaNone: 'No session budget yet \u2014 ask a platform admin, or use a project that sponsors sessions.',
    notSponsored: "This project's owner is not covering sessions here.",
    chargeTo: 'Charge to',
    chargeMine: 'My quota',
    chargeOwner: "The owner's quota",
    sponsorTitle: 'Session costs',
    sponsorBody:
      'Collaborators spend their own session quota here by default. Turn this on to let them charge sessions in this project to you instead — they choose, each time, and you can withdraw it at any point.',
    sponsorLabel: "Cover my collaborators' sessions in this project",
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
    statSessions: 'Replication sessions',
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
    submitHint:
      'Paste an arXiv link, a DOI, or an ACM Digital Library link. The reference resolves to a stable identity first, so repeat replications stay related instead of colliding. Nothing runs, and nothing is spent, until the session starts.',
    submit: 'Submit',
    submitting: 'Submitting…',
    submitError: 'Could not submit — check the URL and try again.',
    activeSession: 'A session for this paper is already running.',
    sessionOptions: 'Session options',
    destinationProject: 'Destination project',
    newProjectOption: 'New project',
    submitNoSpend: 'Nothing runs until the session starts.',
    newProjectDesc: 'Name it, add one or more papers, and invite collaborators. Each paper opens its own replication session.',
    projectNameLabel: 'Project name',
    projectNamePlaceholder: 'Optional — named from the first paper if left blank',
    papersLabel: 'Papers',
    paperPlaceholder: 'arXiv link · DOI · ACM DL',
    addPaper: 'Add another paper',
    removePaper: 'Remove',
    resolve: 'Resolve',
    resolvingPaper: 'Resolving…',
    collaboratorsLabel: 'Collaborators',
    collaboratorsPlaceholder: 'Emails, comma-separated (optional)',
    collaboratorsHint: 'Invited to the project as collaborators.',
    createProjectBtn: 'Create project',
    creatingProject: 'Creating…',
    resolveFailed: 'Could not reach the source — the link may be down or rate-limited.',
    unresolvablePaper: 'Not a DOI, arXiv ID, or URL.',
    noPapers: 'Add at least one paper.',
    errActiveSession: 'Already running.',
    errUnresolvable: 'Unresolvable reference.',
    errEnqueue: 'Could not start.',
    errQuota: 'No sessions left.',
    allPapersFailed: 'No paper could be started — check the references.',
    partialStarted: '{n} session(s) started; some papers were skipped.',
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
    verdict: 'Verdict',
    artifacts: 'Artifacts',
    noVerdict: 'No published session yet — the verdict appears once a session finishes.',
    noArtifacts: 'No artifacts committed yet.',
    selectArtifact: 'Select a file to view it.',
    artifactError: 'Could not load the file.',
    graded: 'Graded',
    redistribution: 'Redistribution',
    methodDigest: 'Method digest',
    repository: 'Repository',
    addReplication: 'Replicate again',
    addReplicationHint: 'Run another replication of a paper in this project.',
    viewTranscript: 'Transcript',
    publishedNote: 'Public — signed off {when}.',
    // members
    members: 'Collaborators',
    noMembers: 'No collaborators yet.',
    inviteEmail: 'colleague@university.edu',
    invite: 'Invite',
    inviteHint: 'Invite by email. They get a message with a link, and can sign in straight away \u2014 running a session here needs budget of their own, or your sponsorship.',
    remove: 'Remove',
    pending: 'pending',
    inviteError: 'Could not add the collaborator.',
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
    profileSaveError: 'Could not save your profile.',
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
    publishError: 'Could not make the project public.',
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
    signedOffOn: 'on {when}',
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
    withdrawError: 'Could not withdraw the publication.',
    // transcript
    transcript: 'Transcript',
    transcriptNotReady: 'The transcript will appear once this session has published.',
    transcriptError: 'Could not load the transcript.',
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

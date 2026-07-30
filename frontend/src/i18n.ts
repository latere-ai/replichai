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
    notInvitedTitle: 'Not yet invited',
    notInvitedBody:
      "You're signed in as {email}, but this is a closed beta and your account has not been granted access yet. Ask a platform admin to add you.",
    // shell
    workspace: 'Workspace',
    navProjects: 'Projects',
    navPublic: 'Public registry',
    newProject: 'New project',
    settings: 'Settings',
    access: 'Access',
    loading: 'Loading…',
    // platform admin (006n)
    platform: 'Platform',
    navAdmin: 'Admin',
    adminIntro:
      'Who may sign in, at what tier, and how much they may run. Everything here is seeded by email — nobody has to have signed in first, and a grant binds to them when they do.',
    platformPolicy: 'Platform policy',
    defaultQuota: 'Default session quota',
    defaultQuotaHint:
      'How many replication sessions an invited person may run. Completed and in-flight sessions count; a failed or stopped one is refunded. Platform admins are unlimited.',
    policyUpdatedBy: 'Last changed by {who}',
    people: 'People',
    noGrants: 'Nobody is invited yet.',
    grant: 'Grant',
    revoke: 'Revoke',
    grantHint: 'A granted person may sign in and create projects. Admins can also grant, revoke, and set platform policy.',
    topup: 'Top up',
    topupN: '+{n}',
    topupPrompt: 'Extra sessions for {email}, above the platform default (0 removes the top-up):',
    adminError: 'That change did not go through.',
    adminNotAdmin: 'Only a platform admin may change this.',
    signOut: 'Sign out',
    theme: 'Theme',
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
    inviteHint: 'Invite by email. They also need a platform invitation from an admin to sign in.',
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
      'Going public opens this project — its sessions, transcripts, and artifacts — to everyone on the platform, and cannot be undone. Only a project that passed the redistribution check can go public.',
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
    signedOffSigner: 'Signer',
    signedOffContact: 'Contact',
    signedOffOrcid: 'ORCID',
    signedOffScholar: 'Scholar',
    // transcript
    transcript: 'Transcript',
    transcriptNotReady: 'The transcript will appear once this session has published.',
    transcriptError: 'Could not load the transcript.',
    events: 'Events',
    eventsCounted: '{n} events',
    showEvents: 'Show events',
    toolsUsed: 'Tools the agent used',
    hideLifecycle: 'Hide lifecycle events',
    thinking: 'thought for a moment (content not exposed by the backend)',
    toolCall: 'tool call',
    toolResult: 'tool result',
    grade: 'Grade',
    noContent: '(no content)',
    usage: 'Token usage',
    usageInput: 'Input',
    usageOutput: 'Output',
    usageCached: 'Cached',
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

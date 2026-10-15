# Security

ReplicHAI runs code it did not write. An agent reads a paper, fetches what
its authors released, and executes it in a sandbox. Around that sandbox the
system holds a credential that can create GitHub repositories, a model
vendor's API key, and a payment processor's webhook secret. This file is what
the project promises about that, what it does not, and how to tell us when it
is wrong.

## Reporting a vulnerability

Report one to security@latere.ai. Do not open a public issue for it. You will
hear back within three business days, and a fix for a high severity issue
ships within thirty days. Credit in the release notes on request.

Fixes go to the latest release, which the
[releases page](https://github.com/latere-ai/replichai/releases) lists.

## Commitments

- What an agent wrote is checked by the system, outside the sandbox, before
  any of it can be made public. A committed tree that carries third-party data
  or a secret, or a transcript that carries a secret, keeps the reproduction
  private.
- Every reproduction repository is created private. Making it public is the
  project owner's own act, recorded with a sign-off.
- The web application fails closed. A request with no grant behind it is
  refused, every operator route is gated, and the development bypass refuses
  to run on an address other than loopback.
- A URL a user supplies is fetched by one fetcher, which refuses private and
  loopback addresses.
- On your own machine (`--runtime local`), no repository credential enters the
  sandbox. The host verifies and pushes the work after the run, with a token
  scoped to that one repository and revoked afterwards.
- A grade the system cannot read is never reported as a pass.

## What is protected, and what proves it

Each row is an asset, the control that protects it, and the tests in this
repository that hold the control to its word.

| Asset | Control | Proved by |
|---|---|---|
| what gets published | the committed tree is verified outside the sandbox; third-party data, an installed dependency tree or a secret bars the reproduction from going public | `TestVerifyTreeFlagsThirdPartyDataAndSecrets`, `TestVerifyTreeFlagsAnInstalledDependencyTree`, `TestFinalizeLiveLeakedTreeBarsPublic` |
| credentials in the evidence | a transcript carrying a secret bars the reproduction from going public, and credential shapes are removed from what is committed | `TestTranscriptLeaksFlagsSecrets`, `TestFinalizeLiveSecretInTranscriptBarsPublic`, `TestCredentialShapesAreRemoved` |
| a reproduction's visibility | every repository is created private | `TestPublishLiveCreatesPrivateRepo` |
| the application's data | the gate fails closed and re-resolves per request, the operator routes are gated, and a stranger cannot tell a private project from a missing one | `TestGateIsFailClosedAndEnumerable`, `TestGateFailsClosedAndReResolves`, `TestOperatorRoutesAreGated`, `TestAStrangerCannotTellAPrivateProjectFromNone` |
| a public deployment | the development bypass refuses a non-loopback address unless explicitly overridden | `TestCheckDevBypass` |
| the network around the server | user-supplied URLs go through one fetcher that refuses private and loopback addresses | `TestServerFetcherRefusesPrivateAddresses`, `TestLoopbackIsRefusedUnlessAllowed` |
| a reader of a published artifact | artifact files are served under a sandboxing content security policy | `TestArtifactTextIsSandboxedToo` |
| the model vendor's API key | it travels in a request header, never in a URL | `TestTheCredentialTravelsInAHeaderNotTheURL` |
| credentials on your own machine | a container launch carries no credential on its command line, recorded events have configured credentials removed, a session's inputs are read-only and outside its workspace, and the grader reads the work it grades without being able to change it | `TestLaunchArgvKeepsCredentialsAndEvidenceOut`, `TestRedactEventRemovesConfiguredCredentials`, `TestAnInputIsReadOnlyAndOutsideTheWorkspace`, `TestOnlyTheJudgeIsReadOnly` |
| the repository a local run pushes to | the push uses a token scoped to one repository and revoked after use | `TestMintRepoTokenIsScopedToOneRepositoryAndRevocable` |
| the credit ledger | a payment webhook with a bad signature is refused, and a retried delivery credits once | `TestStripeWebhookCreditsOnceAndRefusesABadSignature` |
| the verdict | a grade the system cannot read becomes a failure, never a pass | `TestTranslateGradeNeverInventsAPass` |

## What is out of scope

- **The hosted runtime's repository credential.** On the hosted runtime the
  agent commits into the run's repository as it works, so the sandbox holds a
  credential for that repository for the whole run. A GitHub App installation
  token lives at most an hour, shorter than a long run, so this credential is
  not one. An agent that misuses it can write to the run's repository.
- **The host sandbox's isolation.** `--sandbox host` confines the agent with
  the operating system's sandboxing, not a virtual machine. A kernel escape is
  out of scope. Run code you do not trust in `--sandbox container` or on a
  disposable machine.
- **The memory runtime**, a deterministic fake for development and tests. It
  runs nothing and isolates nothing.
- **What a paper's authors released.** ReplicHAI runs it in a sandbox and makes
  no claim that it is safe.
- **Third-party platforms** the system drives: the model vendor's managed
  agent service and its sandbox, the coding agent and the login you use with
  `--runtime local`, your identity provider, GitHub, and the payment
  processor.
- **Your own deployment's configuration.** The commitments describe a
  correctly configured deployment.
- **Denial of service** beyond what the credit meter limits.

## Supply chain

Every push runs a vulnerability check over the Go module graph as part of the
quality gate. Release images are built from a tag by the release workflow.
They are not signed and carry no bill of materials.

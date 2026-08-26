# 045b — The host sandbox

**Status:** built.

**Scope:** the default `Sandbox` driver — the harness already installed on the machine, confined by `srt` (Seatbelt on macOS, bubblewrap with seccomp on Linux) to the session's workspace and an allow-only network. The path policy, the deny list, the network policy, detached process lifetime, the readiness preflight and the message it prints, and `environment.json` — the answer to what an unpinned environment is allowed to leave unrecorded.

**Depends on:** [045a](045a-the-sandbox-is-a-seam.md), and [`anthropic-experimental/sandbox-runtime`](https://github.com/anthropic-experimental/sandbox-runtime) (`srt`).

## Problem

A container gives a boundary and a pinned toolchain together, and charges for both: an image to pull, a virtual machine on macOS, a raw API key instead of the login the operator already has, and a per-session cost that makes twenty papers at once expensive. Most of that is paid for the pinning, and pinning is not what a first local run needs.

What a local run needs is that the agent cannot read the operator's SSH keys, cannot write outside the folder it was given, and cannot reach the network except where it was allowed. `srt` provides exactly that at the OS level, in milliseconds, around a process that is otherwise an ordinary host process — which means the harness's own installation and its own login work unchanged.

The cost is real and is what the rest of this leaf is about: the toolchain is the host's, and the harness's configuration has to be readable for its login to be usable.

## The path policy

One writable root, one readable root, and everything else denied.

| Path | Access | Why |
|---|---|---|
| `<session>/workspace` | read-write | the agent's world, and the repository |
| `<session>/grade` | read-write, judge stage only | the verdict, outside the reproduction ([045f](045f-the-grader-cannot-edit-what-it-grades.md)) |
| `<session>/session` | **denied** | the transcript the agent must not be able to edit |
| the harness's own config (`~/.claude`, `~/.codex`) | read-only | the login already on the machine |
| system toolchain paths (`/usr`, `/bin`, `/opt/homebrew`, `/nix`, …) | read-only | python, node, git, compilers |
| `<session>/tmp`, bound to `TMPDIR` | read-write | downloads, the paper PDF, everything the method says to keep out of the workspace |
| `<session>/venv` | read-write | the session's Python environment, deliberately not inside the repository |
| everything else under `$HOME` | denied | |

The deny list is explicit and independent of the default, because a default that widens by accident is how a rule like this fails: `~/.ssh`, `~/.aws`, `~/.config/gcloud`, `~/.kube`, `~/.netrc`, `~/.gnupg`, `~/.docker/config.json`, `~/.config/replichai` (the cloud token, [045i](045i-signing-in-from-the-terminal.md)), `~/.git-credentials`, and any `.env` anywhere. It is a table with a test that asserts each entry is unreadable from inside a real sandbox, not a comment in a profile generator.

Two consequences worth stating plainly:

**The harness config is readable, and that is the hole.** A model told to read `~/.claude` can read all of it, including other projects' local settings and any history the harness keeps there. That is the price of using the login already on the machine, and the alternative — snapshotting the config into the sandbox — breaks refreshed OAuth tokens on long runs, which is worse. The allowance is narrowed to the harness's own directory and is stated in the run's `environment.json`, so it is a disclosed property of a local run rather than a surprise.

**Global installs fail, and should.** `pip install` outside a virtualenv writes to a denied path. The runtime therefore creates a virtual environment before the first stage and puts it first on `PATH`, so the method's declared packages install somewhere the agent can use.

It goes at `<session>/venv`, **outside the workspace**, not at `workspace/.venv`. The agent is told to commit its work, and one `git add -A` would put a few hundred megabytes of interpreter into the repository's history before any gate saw it — and `VerifyTree` would then flag the archives inside it and bar the repository from ever being made public ([045g](045g-the-host-pushes.md)). Keeping the environment out of the repository is a path choice, which is free; keeping it out by filtering is a rule that has to keep working. The sandbox grants that path read-write already, so nothing about installing changes.

`apt` is not a supported package manager under this driver, and `ValidateCapabilities` refuses a specification that asks for one.

## The network policy

`srt` denies network by default and allows named domains through a host proxy, with wildcards and a precedence rule where denials win. That maps onto `managed.NetworkPolicy` directly:

| `NetworkPolicy.Mode` | driver behaviour |
|---|---|
| `none` | no proxy, no egress |
| `allowlist` | `allowedDomains` from the specification, plus the model endpoint's host |
| `open` | all domains allowed, still through the proxy so the transcript records what was reached |

This is the capability the container driver cannot honestly declare ([045c](045c-the-container-sandbox.md)), and it is the more interesting one for this product: an audit's artifact hunt is a defensible activity precisely when a reader can see where it went. Under `open` the proxy's access log is written to `session/network.log` and summarised in `environment.json`; it is host-owned, so the agent cannot edit it.

The caveats `srt` states are inherited and are not papered over: filtering is by domain and not by content, so a broad allowance like `github.com` permits exfiltration to any repository on it, and domain fronting can evade the filter. The threat model here is a model that makes mistakes, not a model that is trying to escape; a run that needs the stronger claim uses the container driver on a machine that is itself isolated.

## Detached lifetime

The harness is a child process, and `--detach` means it must outlive the CLI that started it. The driver `setsid`s the stage, writes `session/raw/<stage>.log` from its stdout, and records `pid:<pid>@<start-time>` as the handle. Liveness is the pid *and* its start time, because a pid alone is reused and a session that adopted an unrelated process would report a live run that is not there.

`Observe` reads the exit status from a `session/raw/<stage>.status` file the driver's small wrapper writes on exit, so a status is recoverable after the parent is gone — a reaped child tells nobody anything.

## The preflight

`srt` needs installing, and on Linux it needs `bubblewrap`, `socat` and `ripgrep`. A missing dependency is a typed `NotReadyError` carrying remediation, produced by a pure function of `(platform, missing)` so it is testable with nothing installed:

```
local: the host sandbox is not ready.

  srt is not installed.

    npm install -g @anthropic-ai/sandbox-runtime

macOS needs ripgrep as well:

    brew install ripgrep

Then confirm with: srt "echo ok"

Or run in a container instead:  replichai audit … --sandbox container
```

```
local: the host sandbox is missing a dependency.

  bubblewrap: not found
  socat:      not found

    sudo apt-get install -y bubblewrap socat ripgrep      # Debian, Ubuntu
    sudo dnf install -y bubblewrap socat ripgrep          # Fedora, RHEL

Then confirm with: srt "echo ok"
```

```
local: the host sandbox has no harness to run.

  claude: not found on PATH

    npm install -g @anthropic-ai/claude-code
    claude login

Or select another harness:  --harness codex
Or run in a container, which brings its own:  --sandbox container
```

Windows is refused outright, naming WSL2, because `srt` marks it alpha and a boundary that is alpha is not a boundary.

Every message names the container escape hatch, because the second driver exists and an operator stuck on the first one should be told.

## `environment.json`

The container driver pins the environment. This one records it, and the record is the deliverable that makes an unpinned run comparable:

```json
{
  "sandbox": {"driver": "host", "srt": "0.4.1", "network_mode": "open",
              "config_read_allowed": ["~/.claude"]},
  "host": {"os": "darwin", "arch": "arm64", "kernel": "24.6.0"},
  "harness": {"name": "claude-code", "version": "2.1.4", "auth": "subscription"},
  "model": {"provider": "anthropic", "id": "claude-opus-5"},
  "toolchain": {"python": "3.13.2", "node": "22.11.0", "git": "2.47.0"},
  "packages": [{"manager": "pip", "name": "pypdf", "version": "5.1.0"}],
  "network": {"domains_reached": ["arxiv.org", "github.com", "api.anthropic.com"]}
}
```

It is written by the host before the first stage and completed after the last, never by the agent, and finalize commits it as system-authored evidence beside the transcript and `replication.json`. `auth` records *which kind* of credential was used and never the credential.

Two reproductions of one paper that disagree are a finding. Two reproductions on different Python minors that disagree are a finding about Python, and without this file nobody can tell them apart.

## Acceptance criteria

1. The driver passes the shared driver contract suite from [045a](045a-the-sandbox-is-a-seam.md).
2. A stage attempting to write `session/`, read `~/.ssh/id_ed25519`, or read a `.env` outside the workspace fails, asserted by tests that run each one inside a real sandbox on macOS and Linux.
3. Each entry in the deny list has a case in that test. A new entry with no case fails the suite.
4. A stage can read and use the harness's own config directory, and a run using a subscription login completes with no model API key present in its environment.
5. `pip install` without a virtualenv fails inside the sandbox. The session's virtual environment is created at `<session>/venv` before the first stage and is first on `PATH`; a test asserts no path under the workspace is a virtual environment, so the exclusion is a location rather than a filter that could stop working.
6. Under `allowlist`, a request to an unlisted domain fails and a request to a listed one succeeds. Under `open`, `session/network.log` records the hosts reached and is not writable by the stage.
7. A detached stage survives `SIGKILL` of the launching process; `Observe` from a fresh process reports the exit status; a recycled pid is not mistaken for the stage.
8. All four preflight conditions produce a `NotReadyError` whose message names the install commands for the running platform and the `--sandbox container` alternative, tested with nothing installed.
9. Windows is refused at preflight.
10. `environment.json` is written for every run, is committed by finalize, contains no credential, and its `packages` list matches what the venv actually holds.

## Not in scope

- Pinning the host toolchain. That is the other driver ([045c](045c-the-container-sandbox.md)).
- A stronger network claim than domain filtering. The caveats are inherited and disclosed.
- Sandboxing the host push. The push runs after the sandbox is gone and is the host's own act ([045g](045g-the-host-pushes.md)).
- Installing the harness. The preflight says how; it does not run `npm install` on the operator's behalf.

## Outcome

Built as `internal/runtimes/local/host`, and then rewritten in the places a
real `srt` and a real Claude Code disagreed with it. A session now runs end to
end on an operator's own machine with their existing login and no API key. What
that took is worth recording, because none of it was visible from a fixture.

**There is no open network mode, and there cannot be.** srt is allow-only and
refuses a wildcard outright — *"Overly broad patterns like `*.com` or `*` are
not allowed"*. This spec's `open` mapping emitted exactly that, so every
open-network run failed to start. The driver no longer declares the mode, and
`internal/audit` asks for what the runtime can enforce: a declared list of the
places an artifact hunt goes ([`domains.go`](../internal/audit/domains.go)),
recorded in the run's environment record. A bounded hunt finds less than an
unbounded one; the record is how a reader tells the two apart.

**The login is in a keychain, not a file.** The read allowance this spec
designed reaches `~/.claude` and the credential is not there: on macOS it is in
the login Keychain. So a harness declares what it needs of the machine
(`harness.Needs`) and the runtime grants exactly that, read-only, and only to a
session actually using that login. Codex needs nothing extra — its token is a
file — and instead needs its own directory writable, because it writes state
back and will not start otherwise.

**A harness needs a scratch area.** Claude Code creates one per working
directory under the shared temporary root, and without it every shell command
fails before it runs — an agent that can write a file but cannot run one, which
for a reproduction is fatal. It is a declared need now.

**The operator's own instructions are withheld.** `~/.claude/CLAUDE.md`,
`~/.claude/settings.json`, `~/.claude/plugins` and `~/.codex/AGENTS.md` are
denied to every run. They are not the method, and a reproduction that silently
inherits the habits of whoever ran it is not reproducing the paper. This spec
did not think of it; granting the configuration directory for the login is what
raised it.

**TLS termination has to be declared to be usable.** srt publishes its
certificate authority to the standard per-tool trust variables only when
termination is configured, so an undeclared block left every HTTPS client
rejecting the proxy. It is always declared now. pip is the exception that
survives: on macOS it verifies against the system trust store and ignores every
certificate variable, so the package indexes are tunnelled rather than
inspected.

**Two things about the stage itself.** srt parses its own options anywhere in
what it is handed, so the command is separated by `--`; and a stage with no
`PATH` resolves nothing — not even srt, whose shebang needs node — which failed
as an exit 127 with an empty log and is now refused with a reason.

### Still short

**`environment.json` records where a run was *allowed* to go, not where it
went.** The allowlist is in the record; nothing reads srt's proxy log, so
`domains_reached` stays empty.

**The read posture is srt's default, not the tighter one this spec implies.**
Reads are allowed everywhere except the home directory and the named deny list.
A run can read `/usr`, `/etc`, or a checkout outside `$HOME`. Denying `/` and
allowing back a toolchain is a real hardening and a spec of its own.

**The deny list is asserted against the profile, and separately against a real
sandbox for a sample.** Every entry is checked to reach the settings file; a
per-entry test inside a live sandbox is what the driver contract suite would
grow into on a machine that has one.

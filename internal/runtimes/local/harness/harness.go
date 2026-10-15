// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package harness is the interface between the local runtime and one agentic
// coding CLI (045d). A harness contributes what is specific to that program:
// how it is invoked, what environment and configuration it needs, and how to
// read its output. It owns no lifecycle; the sandbox driver starts and stops
// stages.
//
// There is one managed runtime, not one per harness. Runtime names are
// persisted in a session reference and route every later poll, so a name that
// encoded the harness as well as the sandbox would have to be migrated
// whenever either changed.
package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"latere.ai/x/pkg/sanitize"

	"github.com/latere-ai/replichai/internal/managed"
)

// Name identifies one harness, in the spelling an operator uses.
type Name string

// The harnesses this repository ships.
const (
	ClaudeCode Name = "claude-code"
	Codex      Name = "codex"
)

// Stage is one unit of work in a session. The worker produces the
// reproduction, a revision improves it after a failed grading round, and the
// judge grades it from a read-only copy.
type Stage string

// The stages.
const (
	// StageBootstrap prepares the workspace before any agent runs: the
	// language environment and the packages the specification declared. It
	// is a stage rather than a step inside Start so that it is journalled,
	// observable and cancellable like every other unit of work, and so that
	// a failure to install a dependency fails the session with a reason
	// rather than appearing an hour later as an agent that cannot import a
	// package.
	StageBootstrap Stage = "bootstrap"
	StageWorker    Stage = "worker"
	StageRevision  Stage = "revision"
	StageJudge     Stage = "judge"
)

// Writes reports whether a stage may write the workspace. Only the judge may
// not. This is the mechanism that prevents the grader from editing what it
// grades (045f).
func (s Stage) Writes() bool { return s != StageJudge }

// Agentic reports whether a stage runs a harness. The bootstrap does not; it
// runs a fixed script the runtime builds, and no model is involved.
func (s Stage) Agentic() bool { return s != StageBootstrap }

// Dialect is the wire protocol a model endpoint speaks. A harness speaks one
// family of dialects, and the model catalog refuses a mismatched pairing
// rather than letting a run fail with a 400 response after it has started.
type Dialect string

// The dialects.
const (
	DialectAnthropic       Dialect = "anthropic"
	DialectOpenAIResponses Dialect = "openai-responses"
	DialectOpenAIChat      Dialect = "openai-chat"
)

// AuthKind is the kind of credential that reaches the model endpoint. It is
// recorded in a run's environment record; the credential itself never is.
type AuthKind string

// The credential kinds. AuthHarness is the harness's own login. It needs no
// key at all, which is why the host sandbox is the default.
const (
	AuthHarness AuthKind = "harness"
	AuthAPIKey  AuthKind = "api-key"
	AuthNone    AuthKind = "none"
)

// Gateway describes where a model is reached and how. Key is resolved from the
// environment at composition and never travels in a session specification,
// because a specification with an open network may not carry secrets.
type Gateway struct {
	// Name is the operator's label for this endpoint.
	Name string
	// BaseURL is the endpoint, empty when the harness uses its own default.
	BaseURL string
	// Dialect is the wire protocol.
	Dialect Dialect
	// Auth says which credential kind applies.
	Auth AuthKind
	// Key is the resolved credential, empty for AuthHarness and AuthNone.
	Key string
}

// Paths are the directories a stage may use, as host paths. A driver maps them
// into whatever the stage sees.
type Paths struct {
	// Workspace is the agent's work, and the repository.
	Workspace string
	// Tmp is where downloads go. It is deliberately outside the repository.
	Tmp string
	// HarnessTmp is the temporary root the harness itself is pointed at. It
	// is inside Tmp, so the paths the harness derives from it are in the
	// session.
	HarnessTmp string
	// Home is the harness's own configuration directory for this session.
	Home string
	// Evidence is the read-only workspace the judge grades.
	Evidence string
	// Grade is where the judge writes its verdict, and the only path it
	// may write.
	Grade string
}

// Request is everything a harness needs to build one stage. It is assembled by
// the runtime from the session specification; a harness never sees the
// specification itself.
type Request struct {
	// Instructions is the worker's durable role.
	Instructions string
	// Task is this stage's request.
	Task string
	// Model is the exact model the session resolved to.
	Model managed.ModelRef
	// Gateway is where to reach it.
	Gateway Gateway
	// Paths are the directories this stage may use.
	Paths Paths
}

// File is a configuration file written before a stage starts. Its path is
// always outside the workspace, because a file written inside it would be
// committed by the agent and published as part of the reproduction.
type File struct {
	Path string
	Mode uint32
	Body []byte
}

// Needs describes what a harness requires of the machine it runs on beyond
// the session's own directories.
//
// A harness is an installed program, and two of its requirements cannot be
// inferred. First, the location of an existing login differs between
// harnesses: Codex keeps its token in a file under its own configuration
// directory, which the sandbox already grants, while Claude Code on macOS
// keeps its credential in the login Keychain, which is a different file.
// Second, a harness keeps a scratch area of its own outside the workspace.
// Claude Code creates one per working directory under the temporary root, and
// without it every shell command fails before it runs.
type Needs struct {
	// Login lists the paths the operator's existing login lives in, and
	// whether the harness must also write them to run with that login. They
	// are granted only when a session uses that login; a session that
	// carries its own credential gets none of them. Every granted path is
	// disclosed in the run's environment record, because on Linux the
	// credential file and the operator's whole conversation history are in
	// the same directory.
	Login []Grant
	// Writes are paths the harness needs to write in order to work at all,
	// outside the session's own directories and outside its login. They are
	// granted to every stage, because a harness that cannot run a command
	// cannot do its job.
	Writes []string
	// TLSPassthrough lists domains this harness must reach without the
	// sandbox terminating TLS. A sandbox that inspects egress does so by
	// intercepting it with its own certificate authority, which a client
	// that does not read the injected trust store rejects. Rust and Go
	// binaries typically reject it, where a Node binary accepts it. These
	// domains are still subject to the allowlist; only the inspection is
	// skipped.
	TLSPassthrough []string
	// Denies are paths that must stay unreadable even though a broader
	// grant covers them. Without them, the operator's standing instructions
	// would be loaded into every reproduction as if they were the method,
	// and the operator's plugins would run code the method never asked for.
	Denies []string
}

// Grant is one path outside the session that a harness's login needs. It is
// read-only unless Write is set, and Write is set only where the harness
// refuses to start without write access.
type Grant struct {
	Path  string
	Write bool
}

// LoginPaths returns the paths a set of needs would open for the login,
// sorted, so that the environment record lists exactly what was granted in a
// stable order.
func LoginPaths(needs Needs) []string {
	paths := make([]string, 0, len(needs.Login))
	for _, grant := range needs.Login {
		paths = append(paths, grant.Path)
	}
	slices.Sort(paths)
	return paths
}

// Machine describes what a harness needs to know about the machine it runs on
// in order to report its needs. It is passed in rather than read from the
// process, because this package is behind the runtime interface and
// configuration arrives from the composition root.
type Machine struct {
	// Platform is the operating system, as Go names it.
	Platform string
	// Home is the operator's home directory.
	Home string
	// TempDir is the machine's shared temporary root, /tmp on a Unix
	// system. It is deliberately not the value of TMPDIR. A harness puts its
	// scratch area under the shared root regardless of what a session sets
	// TMPDIR to, so a grant derived from TMPDIR would name a directory the
	// harness never uses.
	TempDir string
	// UID is the operator's user id, which a harness uses to name a scratch
	// area that belongs to this user and not another.
	UID int
}

// Harness is one agentic CLI the runtime can drive inside a sandbox.
type Harness interface {
	// Name is the canonical harness name.
	Name() Name

	// Binary is the executable the sandbox must be able to run. The
	// preflight uses it to report what to install.
	Binary() string

	// Dialects returns the wire protocols this harness speaks. The model
	// catalog refuses a gateway outside this set.
	Dialects() []Dialect

	// VersionArgv is the command that prints the installed version, so that
	// a run records what produced it.
	VersionArgv() []string

	// Argv returns the command one stage runs.
	Argv(Stage, Request) ([]string, error)

	// Env returns the environment the stage needs, and the subset of its
	// keys whose values are credentials and must not reach a command line.
	Env(Stage, Request) (map[string]string, []string, error)

	// Files returns the configuration files to write before the stage
	// starts.
	Files(Stage, Request) ([]File, error)

	// Needs reports what this harness requires of the machine: where the
	// operator's existing login lives, where the harness's own scratch area
	// is, and what to withhold from the run regardless of what else is
	// granted.
	Needs(Machine) Needs

	// Decode turns one line of the harness's stream into zero or more
	// normalized events. stream is the stage's unique name rather than its
	// role, because one session may run several judges and ids derived from
	// the role alone would collide. An unrecognized line becomes one event
	// of kind KindRaw carrying the line; it is never dropped.
	Decode(stream string, index int, line []byte) ([]managed.Event, error)
}

// KindRaw is the event kind for a stream line this build does not recognize.
// The set of event kinds is deliberately open, and a transcript that silently
// dropped what it could not parse would not be evidence.
const KindRaw managed.EventKind = "raw"

// KindResult is a harness's own end-of-stage summary: its stop reason and its
// cumulative usage.
const KindResult managed.EventKind = "result"

// MaxContentBytes bounds one event's rendered content. A tool result larger
// than this is truncated here and kept whole in the stage's raw log, so the
// normalized transcript stays readable and nothing is lost.
const MaxContentBytes = 256 << 10

// EventID derives a stable id from a line's position in its stage's stream.
// The id is derived from position rather than content because two identical
// tool results are two events, and a content hash would merge them. Re-reading
// a stream therefore neither duplicates an entry nor changes its meaning.
// stream is the stage's unique name, so a session that grades twice does not
// issue the same id twice.
func EventID(stream string, index int) string { return fmt.Sprintf("%s-%d", stream, index) }

// Raw preserves a stream line on an event. A json.RawMessage must hold valid
// JSON or the event cannot be encoded, so a line that is not JSON, such as a
// shell error or a stray print from the harness, is stored as a JSON string
// rather than as bytes that would fail to marshal and make the whole
// transcript unencodable.
func Raw(line []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(line)
	if json.Valid(trimmed) {
		return json.RawMessage(append([]byte(nil), trimmed...))
	}
	encoded, err := json.Marshal(string(trimmed))
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// PlainEvent returns the event for one line of output from a stage that runs
// no harness. This is the bootstrap, whose output comes from a shell rather
// than a model.
func PlainEvent(stream string, index int, line []byte) managed.Event {
	return managed.Event{
		ID: EventID(stream, index), Role: managed.RoleSystem, Kind: KindRaw,
		Content: Truncate(strings.TrimSpace(string(line))), Raw: Raw(line),
	}
}

// Truncate shortens content to MaxContentBytes and marks what it removed. The
// marker names the raw log, because that is where the whole value remains.
func Truncate(content string) string {
	if len(content) <= MaxContentBytes {
		return content
	}
	kept := sanitize.TruncateBytes(content, MaxContentBytes)
	return kept +
		fmt.Sprintf("\n…[%d bytes elided; the whole value is in this stage's raw log]", len(content)-len(kept))
}

// Registry is the set of harnesses a build can drive. Like the runtime
// catalog, it is fixed at construction: what a binary can run is decided by
// what its composition root passes in.
type Registry struct{ byName map[Name]Harness }

// NewRegistry returns a registry over the given harnesses. It refuses a
// duplicate name rather than letting one harness shadow another.
func NewRegistry(harnesses ...Harness) (*Registry, error) {
	registry := &Registry{byName: make(map[Name]Harness, len(harnesses))}
	for _, h := range harnesses {
		name := Canonical(string(h.Name()))
		if name == "" || name != h.Name() {
			return nil, fmt.Errorf("harness name %q is not canonical", h.Name())
		}
		if _, exists := registry.byName[name]; exists {
			return nil, fmt.Errorf("duplicate harness %q", name)
		}
		registry.byName[name] = h
	}
	return registry, nil
}

// Canonical normalizes operator-supplied text to the one spelling a harness
// name may take.
func Canonical(raw string) Name { return Name(strings.ToLower(strings.TrimSpace(raw))) }

// Names lists the installed harnesses, sorted, so that a refusal offers them
// in a stable order.
func (r *Registry) Names() []Name {
	names := make([]Name, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Lookup resolves operator-supplied text to an installed harness. When the
// name is unknown, the error lists the installed harnesses.
func (r *Registry) Lookup(raw string) (Harness, error) {
	name := Canonical(raw)
	if h, ok := r.byName[name]; ok {
		return h, nil
	}
	choices := make([]string, 0, len(r.byName))
	for _, installed := range r.Names() {
		choices = append(choices, string(installed))
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("harness %q is unavailable; none are installed", name)
	}
	return nil, fmt.Errorf("harness %q is unavailable; available: %s", name, strings.Join(choices, ", "))
}

// Speaks reports whether a harness can talk to a gateway's dialect.
func Speaks(h Harness, dialect Dialect) bool {
	return slices.Contains(h.Dialects(), dialect)
}

// RevisionHeader introduces a grader's feedback in a revision stage's task. It
// is fixed text so that the revision reads in the transcript as its own
// request rather than as a modified original.
const RevisionHeader = "\n\n--- The grader rejected the previous attempt. Its reasoning follows; address it. ---\n\n"

// Outside reports whether a file destination is outside the workspace. No
// harness may write configuration into the workspace, because the agent
// commits what is there and the method's own text would be published as part
// of the paper's reproduction.
func Outside(workspace, destination string) bool {
	workspace = path.Clean(workspace)
	destination = path.Clean(destination)
	return destination != workspace && !strings.HasPrefix(destination, workspace+"/")
}

// Fold accumulates one event's usage report into a session total. A harness
// reports running totals rather than deltas: Claude Code's result line carries
// the session's total, and each assistant line carries its message's total.
// The fold is therefore an elementwise maximum rather than a sum. That is
// monotonic by construction, which is what the contract requires of two
// consecutive observations, and it cannot double-count a repeated total.
func Fold(total, next managed.Usage) managed.Usage {
	if next.Model.Valid() {
		total.Model = next.Model
	}
	total.InputFresh = max(total.InputFresh, next.InputFresh)
	total.InputCached = max(total.InputCached, next.InputCached)
	total.CacheWrite = max(total.CacheWrite, next.CacheWrite)
	total.CacheWrite5m = max(total.CacheWrite5m, next.CacheWrite5m)
	total.CacheWrite1h = max(total.CacheWrite1h, next.CacheWrite1h)
	total.Output = max(total.Output, next.Output)
	for meter, value := range next.Extra {
		if total.Extra == nil {
			total.Extra = make(map[managed.Meter]int64, len(next.Extra))
		}
		total.Extra[meter] = max(total.Extra[meter], value)
	}
	for name, value := range next.Details {
		if total.Details == nil {
			total.Details = make(map[string]int64, len(next.Details))
		}
		total.Details[name] = max(total.Details[name], value)
	}
	return total
}

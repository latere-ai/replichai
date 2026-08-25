// Package harness is the seam between the local runtime and one agentic coding
// CLI (045d). A harness contributes what is specific to that program — how it
// is invoked, what environment and configuration it needs, and how to read what
// it says — and owns no lifecycle: the sandbox driver starts and stops stages.
//
// There is one managed runtime, not one per harness. Runtime names are
// persisted in a session reference and route every later poll, so a name
// encoding the harness as well as the sandbox would have to be migrated
// whenever either changed.
package harness

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
)

// Name identifies one harness as an operator spells it.
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
	StageWorker   Stage = "worker"
	StageRevision Stage = "revision"
	StageJudge    Stage = "judge"
)

// Writes reports whether a stage may write the workspace. Only the judge may
// not, which is the whole mechanism behind a grader that cannot edit what it
// grades (045f).
func (s Stage) Writes() bool { return s != StageJudge }

// Dialect is the wire protocol a model endpoint speaks. A harness speaks one
// family of them, and the model catalogue refuses a pairing that disagrees
// rather than letting a run fail on a 400 forty seconds in.
type Dialect string

// The dialects.
const (
	DialectAnthropic       Dialect = "anthropic"
	DialectOpenAIResponses Dialect = "openai-responses"
	DialectOpenAIChat      Dialect = "openai-chat"
)

// AuthKind is which sort of credential reaches the model endpoint. It is
// recorded in a run's environment record; the credential itself never is.
type AuthKind string

// The credential kinds. AuthHarness is the harness's own login — the reason
// the host sandbox is the default, because it needs no key at all.
const (
	AuthHarness AuthKind = "harness"
	AuthAPIKey  AuthKind = "api-key"
	AuthNone    AuthKind = "none"
)

// Gateway is where a model is reached and how. Key is resolved from the
// environment at composition and never travels in a session specification: a
// specification with an open network may not carry secrets.
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
	// Tmp is where downloads go, deliberately outside the repository.
	Tmp string
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

// File is a configuration file written before a stage starts. Destination is
// always outside the workspace: a file written inside it would be committed by
// the agent and published as part of the reproduction.
type File struct {
	Path string
	Mode uint32
	Body []byte
}

// Harness is one agentic CLI the runtime can drive inside a sandbox.
type Harness interface {
	// Name is the canonical harness name.
	Name() Name

	// Binary is the executable the sandbox must be able to run, used by
	// the preflight to say what to install.
	Binary() string

	// Dialects are the wire protocols this harness speaks. The model
	// catalogue refuses a gateway outside this set.
	Dialects() []Dialect

	// VersionArgv is the command that prints the installed version, so a
	// run records what actually produced it.
	VersionArgv() []string

	// Argv is what one stage runs.
	Argv(Stage, Request) ([]string, error)

	// Env is the environment the stage needs, and the subset of its keys
	// whose values are credentials and must not reach a command line.
	Env(Stage, Request) (map[string]string, []string, error)

	// Files are the configuration files to write before the stage starts.
	Files(Stage, Request) ([]File, error)

	// Decode turns one line of the harness's stream into zero or more
	// normalized events. stream is the stage's unique name rather than its
	// role: one session may run several judges, and ids derived from the
	// role alone would collide. An unrecognised line becomes one event of
	// kind KindRaw carrying the line, never nothing.
	Decode(stream string, index int, line []byte) ([]managed.Event, error)
}

// KindRaw is the event kind for a stream line this build does not recognise.
// The set of event kinds is deliberately open, and a transcript that silently
// drops what it cannot parse is not evidence.
const KindRaw managed.EventKind = "raw"

// KindResult is a harness's own end-of-stage summary: its stop reason and its
// cumulative usage.
const KindResult managed.EventKind = "result"

// MaxContentBytes bounds one event's rendered content. A tool result larger
// than this is truncated here and kept whole in the stage's raw log, so the
// normalized transcript stays readable without anything being destroyed.
const MaxContentBytes = 256 << 10

// EventID derives a stable id from a line's position in its stage's stream.
// Position rather than content: two identical tool results are two events, and
// a content hash would merge them. Re-reading a stream therefore neither
// duplicates an entry nor changes what one means. stream is the stage's unique
// name, so a session that grades twice does not issue one id twice.
func EventID(stream string, index int) string { return fmt.Sprintf("%s-%d", stream, index) }

// Truncate shortens content to MaxContentBytes, marking what it removed. The
// marker names the raw log, because that is where the whole value still is.
func Truncate(content string) string {
	if len(content) <= MaxContentBytes {
		return content
	}
	return content[:MaxContentBytes] +
		fmt.Sprintf("\n…[%d bytes elided; the whole value is in this stage's raw log]", len(content)-MaxContentBytes)
}

// Registry is the set of harnesses a build can drive, fixed at construction the
// way the runtime catalogue is: what a binary can run is decided by what its
// composition root passes in.
type Registry struct{ byName map[Name]Harness }

// NewRegistry returns a registry over the given harnesses, refusing a
// duplicate name rather than letting one shadow another.
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

// Canonical folds operator-supplied text into the one spelling a harness name
// may take.
func Canonical(raw string) Name { return Name(strings.ToLower(strings.TrimSpace(raw))) }

// Names lists the installed harnesses, sorted, so a refusal offers them in a
// stable order.
func (r *Registry) Names() []Name {
	names := make([]Name, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// Lookup resolves operator-supplied text to an installed harness, and tells
// the caller what they could have asked for instead of leaving them to guess.
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
	for _, candidate := range h.Dialects() {
		if candidate == dialect {
			return true
		}
	}
	return false
}

// RevisionHeader introduces a grader's feedback in a revision stage's task. It
// is fixed text so the revision reads in the transcript as its own request
// rather than as a mutated original.
const RevisionHeader = "\n\n--- The grader rejected the previous attempt. Its reasoning follows; address it. ---\n\n"

// InstructionHeader introduces the worker's standing role where a harness has
// no system-prompt mechanism to carry it. A harness that uses this records it,
// so a reader can see which form was used.
const InstructionHeader = "--- Standing instructions ---\n\n"

// Prepend combines instructions and task for a harness with no system-prompt
// flag. The contract permits combining the two on the wire; it does not permit
// reordering them, so the instructions always come first.
func Prepend(instructions, task string) string {
	return InstructionHeader + instructions + "\n\n--- Task ---\n\n" + task
}

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
// reports its own running totals rather than deltas — Claude Code's result
// line carries the session's, and each assistant line carries its message's —
// so the fold is an elementwise maximum rather than a sum. That is monotonic
// by construction, which is what the contract demands of two consecutive
// observations, and it cannot double-count a total that is repeated.
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

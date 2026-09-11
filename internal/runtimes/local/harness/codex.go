// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
)

// CodexHarness drives the Codex CLI. It speaks the OpenAI dialects, so it is
// the harness an open-weights model behind an OpenAI-compatible gateway pairs
// with, and it carries both its role and its model provider in a configuration
// file written outside the workspace.
type CodexHarness struct{}

// NewCodex returns the Codex adapter.
func NewCodex() CodexHarness { return CodexHarness{} }

// Name is the canonical harness name.
func (CodexHarness) Name() Name { return Codex }

// Binary is the executable a preflight looks for.
func (CodexHarness) Binary() string { return "codex" }

// Dialects are the two OpenAI wire protocols. Which one a gateway speaks is
// the operator's declaration, and it reaches the harness as wire_api.
func (CodexHarness) Dialects() []Dialect {
	return []Dialect{DialectOpenAIResponses, DialectOpenAIChat}
}

// VersionArgv prints the installed version, which a run records.
func (CodexHarness) VersionArgv() []string { return []string{"codex", "--version"} }

// CodexKeyEnv is the environment variable the generated provider block reads
// its credential from. It is a fixed name so the configuration file never has
// to hold the credential itself.
const CodexKeyEnv = "REPLICHAI_MODEL_KEY"

// Argv is the stage's command. The harness's own sandbox is switched off
// because this stage is already inside a real one; a second boundary the model
// can be talked past adds nothing, and the runtime refuses to build this argv
// without a driver confining the stage.
func (CodexHarness) Argv(stage Stage, request Request) ([]string, error) {
	workdir, task, err := stageWork(stage, request)
	if err != nil {
		return nil, err
	}
	// --skip-git-repo-check: Codex refuses to run in a directory it does not
	// consider trusted, and the workspace is a fresh repository it has never
	// seen. The boundary here is the sandbox, not Codex's own trust list.
	argv := []string{"codex", "exec", "--json", "--skip-git-repo-check",
		"--sandbox", "danger-full-access", "--cd", workdir}
	if request.Model.ID != "" {
		argv = append(argv, "--model", request.Model.ID)
	}
	return append(argv, "--", codexTask(request, task)), nil
}

// codexTask is the prompt, with the standing instructions in front of it where
// no file carried them.
//
// Codex reads a role from AGENTS.md under CODEX_HOME and has no flag for one.
// A run on the operator's own login keeps the operator's own CODEX_HOME, and
// replichai will not write into it — so Files writes nothing, and the
// instructions have no file to arrive in. Three runs found that out: with no
// standing instructions the agent never learned the workspace protocol, wrote
// its reproduction into a subdirectory, committed nothing, and left an empty
// repository behind a session that the rubric had passed.
//
// Codex documents the prompt argument as the agent's initial instructions, so
// that is where they go. The role is the same text either way; only the
// channel differs, because the two harnesses read a role from different
// places.
func codexTask(request Request, task string) string {
	instructions := strings.TrimSpace(request.Instructions)
	if instructions == "" || request.Gateway.Auth != AuthHarness {
		return task
	}
	return instructions + "\n\n" + task
}

// Env points Codex at its session-scoped configuration and supplies the
// gateway credential under the fixed name the provider block reads.
func (CodexHarness) Env(_ Stage, request Request) (map[string]string, []string, error) {
	env := map[string]string{}
	// Codex keeps its token in a file under CODEX_HOME. Redirecting it to a
	// session-scoped directory is how a session gets an isolated
	// configuration — and it is also how a session loses the login, because
	// an empty directory holds no token and the endpoint answers 401. The
	// redirect happens only where a credential is being supplied instead.
	if request.Gateway.Auth != AuthHarness {
		env["CODEX_HOME"] = request.Paths.Home
	}
	switch request.Gateway.Auth {
	case AuthHarness, AuthNone:
		return env, nil, nil
	case AuthAPIKey:
		if strings.TrimSpace(request.Gateway.Key) == "" {
			return nil, nil, fmt.Errorf("gateway %q declares an api key and has none", request.Gateway.Name)
		}
		env[CodexKeyEnv] = request.Gateway.Key
		return env, []string{CodexKeyEnv}, nil
	default:
		return nil, nil, fmt.Errorf("unknown auth kind %q", request.Gateway.Auth)
	}
}

// Files writes the configuration Codex reads: the model provider block that
// points at the operator's gateway, and the standing instructions. Both go to
// CODEX_HOME, outside the workspace, so neither is committed by the agent and
// published as part of the reproduction.
func (CodexHarness) Files(_ Stage, request Request) ([]File, error) {
	// Where the login is the operator's own, so is the configuration: the
	// session writes none, because nothing would read it.
	if request.Gateway.Auth == AuthHarness {
		return nil, nil
	}
	files := []File{{
		Path: filepath.Join(request.Paths.Home, "config.toml"),
		Mode: 0o600,
		Body: []byte(codexConfig(request.Gateway)),
	}}
	if instructions := strings.TrimSpace(request.Instructions); instructions != "" {
		files = append(files, File{
			Path: filepath.Join(request.Paths.Home, "AGENTS.md"),
			Mode: 0o600,
			Body: []byte(instructions + "\n"),
		})
	}
	return files, nil
}

// codexConfig renders the provider block. A gateway with no base URL leaves
// Codex on its own default, which is the vendor endpoint.
func codexConfig(gateway Gateway) string {
	if gateway.BaseURL == "" {
		return "# replichai: the harness keeps its own model provider and its own login.\n"
	}
	wire := "responses"
	if gateway.Dialect == DialectOpenAIChat {
		wire = "chat"
	}
	key := ""
	if gateway.Auth == AuthAPIKey {
		key = fmt.Sprintf("env_key = %q\n", CodexKeyEnv)
	}
	return fmt.Sprintf(`model_provider = "replichai"

[model_providers.replichai]
name = "replichai gateway"
base_url = %q
wire_api = %q
%s`, gateway.BaseURL, wire, key)
}

// Needs is what Codex requires of the machine: its configuration directory,
// where its token lives and where it writes its own state — its aliases, its
// app-server, its session records — and fails to start where it cannot; and a
// scratch area under the temporary root.
//
// The configuration directory is a login grant, not a standing one: a session
// carrying its own credential has CODEX_HOME redirected into the session and
// never touches the operator's, so it is granted nothing there.
//
// The operator's standing instructions are denied for the same reason they are
// for any harness: they are not the method.
func (CodexHarness) Needs(machine Machine) Needs {
	// Codex is a Rust binary and does not read an injected certificate
	// authority, so a sandbox that terminates TLS to inspect egress breaks
	// every request it makes. Its own endpoints are tunnelled opaquely
	// instead — still allowlisted, just not inspected.
	needs := Needs{TLSPassthrough: []string{
		"chatgpt.com", "*.chatgpt.com", "api.openai.com", "auth.openai.com",
	}}
	if machine.TempDir != "" {
		needs.Writes = append(needs.Writes,
			filepath.Join(machine.TempDir, fmt.Sprintf("codex-%d", machine.UID)))
	}
	if machine.Home != "" {
		needs.Login = []Grant{{Path: filepath.Join(machine.Home, ".codex"), Write: true}}
		needs.Denies = []string{filepath.Join(machine.Home, ".codex", "AGENTS.md")}
	}
	return needs
}

// codexLine is the subset of the stream this adapter reads.
//
// Codex emits a flat envelope: a type, and for the events that carry one, an
// item. Older builds wrapped the same content in a msg field, so both are
// accepted and anything else becomes a raw event rather than a guess.
type codexLine struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Msg      json.RawMessage `json:"msg"`
	Item     json.RawMessage `json:"item"`
	Usage    *codexTokens    `json:"usage"`
}

// codexItem is one thing the agent did.
type codexItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Text     string `json:"text"`
	Message  string `json:"message"`
	Command  string `json:"command"`
	Output   string `json:"aggregated_output"`
	ExitCode *int   `json:"exit_code"`
	Status   string `json:"status"`
	Path     string `json:"path"`
	Reason   string `json:"reason"`
}

// codexTokens is what a turn consumed, as Codex reports it.
type codexTokens struct {
	InputTokens          int64 `json:"input_tokens"`
	CachedInputTokens    int64 `json:"cached_input_tokens"`
	CacheWriteTokens     int64 `json:"cache_write_input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	ReasoningOutputTokes int64 `json:"reasoning_output_tokens"`
}

// usage maps Codex's accounting onto the contract's buckets. Cached input is
// its own bucket, and reasoning output is decomposition of the output bucket
// rather than a bucket of its own: it is already counted in Output, and
// pricing never iterates Details.
func (t *codexTokens) usage(model managed.ModelRef) managed.Usage {
	if t == nil {
		return managed.Usage{Model: model}
	}
	usage := managed.Usage{
		Model:       model,
		InputFresh:  t.InputTokens,
		InputCached: t.CachedInputTokens,
		CacheWrite:  t.CacheWriteTokens,
		Output:      t.OutputTokens,
	}
	if t.ReasoningOutputTokes > 0 {
		usage.Details = map[string]int64{"reasoning_output": t.ReasoningOutputTokes}
	}
	return usage
}

// Decode reads one line of the stream.
func (CodexHarness) Decode(stream string, index int, line []byte) ([]managed.Event, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, nil
	}
	event := managed.Event{ID: EventID(stream, index), Time: time.Now().UTC(), Raw: Raw(line)}
	raw := func() []managed.Event {
		event.Role, event.Kind, event.Content = managed.RoleSystem, KindRaw, Truncate(trimmed)
		return []managed.Event{event}
	}

	var envelope codexLine
	if err := json.Unmarshal(line, &envelope); err != nil {
		//nolint:nilerr // an unparsable line is evidence, not a failure
		return raw(), nil
	}
	switch envelope.Type {
	case "thread.started", "turn.started", "session_configured":
		event.Role, event.Kind = managed.RoleSystem, managed.EventKind("system")
		event.Content = Truncate(strings.TrimSpace(envelope.Type + " " + envelope.ThreadID))
		return []managed.Event{event}, nil
	case "turn.completed", "turn.failed":
		event.Role, event.Kind = managed.RoleSystem, KindResult
		event.Content = Truncate(envelope.Type)
		usage := envelope.Usage.usage(managed.ModelRef{})
		event.Usage = &usage
		return []managed.Event{event}, nil
	}

	body := envelope.Item
	if len(body) == 0 {
		body = envelope.Msg
	}
	if len(body) == 0 {
		return raw(), nil
	}
	var item codexItem
	if err := json.Unmarshal(body, &item); err != nil {
		//nolint:nilerr // an unparsable line is evidence, not a failure
		return raw(), nil
	}
	// A started item is the same item as its completion, and the completion
	// carries the result; recording both would double the transcript and
	// say nothing twice.
	if envelope.Type == "item.started" && item.Type != "command_execution" {
		return nil, nil
	}
	switch item.Type {
	case "agent_message", "assistant_message":
		event.Role, event.Kind = managed.RoleAgent, managed.KindMessage
		event.Content = Truncate(firstNonEmpty(item.Text, item.Message))
	case "reasoning", "agent_reasoning":
		event.Role, event.Kind = managed.RoleAgent, managed.KindThinking
		event.Content = Truncate(firstNonEmpty(item.Text, item.Message))
	case "command_execution":
		event.Tool = "exec"
		if envelope.Type == "item.started" || item.ExitCode == nil {
			event.Role, event.Kind = managed.RoleAgent, managed.KindToolUse
			event.Content = Truncate(item.Command)
			break
		}
		event.Role, event.Kind = managed.RoleUser, managed.KindToolResult
		event.Content = Truncate(codexCommandResult(item))
	case "file_change", "patch_apply":
		event.Role, event.Kind, event.Tool = managed.RoleAgent, managed.KindToolUse, "edit"
		event.Content = Truncate(firstNonEmpty(item.Path, item.Text))
	case "error":
		event.Role, event.Kind = managed.RoleSystem, KindResult
		event.Content = Truncate(firstNonEmpty(item.Message, item.Reason, item.Text))
	default:
		return raw(), nil
	}
	return []managed.Event{event}, nil
}

// codexCommandResult renders an executed command's outcome, keeping the exit
// code visible: a tool result that shows only output hides the failure.
func codexCommandResult(item codexItem) string {
	var b strings.Builder
	if item.ExitCode != nil {
		fmt.Fprintf(&b, "exit %d\n", *item.ExitCode)
	}
	b.WriteString(item.Output)
	return strings.TrimSpace(b.String())
}

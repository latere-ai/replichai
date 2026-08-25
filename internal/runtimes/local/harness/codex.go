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
	argv := []string{"codex", "exec", "--json", "--sandbox", "danger-full-access", "--cd", workdir}
	if request.Model.ID != "" {
		argv = append(argv, "--model", request.Model.ID)
	}
	return append(argv, "--", task), nil
}

// Env points Codex at its session-scoped configuration and supplies the
// gateway credential under the fixed name the provider block reads.
func (CodexHarness) Env(_ Stage, request Request) (map[string]string, []string, error) {
	env := map[string]string{"CODEX_HOME": request.Paths.Home}
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

// codexLine is the subset of the stream this adapter reads. Codex has carried
// more than one envelope across versions — a msg-wrapped event and a flat one
// — so both are accepted and anything else becomes a raw event.
type codexLine struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Msg  json.RawMessage `json:"msg"`
	Item json.RawMessage `json:"item"`
}

type codexEvent struct {
	Type     string          `json:"type"`
	Message  string          `json:"message"`
	Text     string          `json:"text"`
	Command  []string        `json:"command"`
	Stdout   string          `json:"stdout"`
	Stderr   string          `json:"stderr"`
	ExitCode *int            `json:"exit_code"`
	Reason   string          `json:"reason"`
	Info     *codexTokenInfo `json:"info"`
}

type codexTokenInfo struct {
	Total *codexTokens `json:"total_token_usage"`
	Last  *codexTokens `json:"last_token_usage"`
}

type codexTokens struct {
	InputTokens          int64 `json:"input_tokens"`
	CachedInputTokens    int64 `json:"cached_input_tokens"`
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
	event := managed.Event{ID: EventID(stream, index), Time: time.Now().UTC(), Raw: json.RawMessage(append([]byte(nil), line...))}
	raw := func() []managed.Event {
		event.Role, event.Kind, event.Content = managed.RoleSystem, KindRaw, Truncate(trimmed)
		return []managed.Event{event}
	}

	var envelope codexLine
	if err := json.Unmarshal(line, &envelope); err != nil {
		//nolint:nilerr // an unparsable line is evidence, not a failure
		return raw(), nil
	}
	body := envelope.Msg
	if len(body) == 0 {
		body = envelope.Item
	}
	if len(body) == 0 {
		body = line
	}
	var parsed codexEvent
	if err := json.Unmarshal(body, &parsed); err != nil {
		//nolint:nilerr // an unparsable line is evidence, not a failure
		return raw(), nil
	}
	kind := firstNonEmpty(parsed.Type, envelope.Type)
	model := managed.ModelRef{}
	switch kind {
	case "agent_message", "assistant_message", "agent_message_delta":
		event.Role, event.Kind = managed.RoleAgent, managed.KindMessage
		event.Content = Truncate(firstNonEmpty(parsed.Message, parsed.Text))
	case "agent_reasoning", "reasoning":
		event.Role, event.Kind = managed.RoleAgent, managed.KindThinking
		event.Content = Truncate(firstNonEmpty(parsed.Text, parsed.Message))
	case "exec_command_begin", "command_begin":
		event.Role, event.Kind, event.Tool = managed.RoleAgent, managed.KindToolUse, "exec"
		event.Content = Truncate(strings.Join(parsed.Command, " "))
	case "exec_command_end", "command_end":
		event.Role, event.Kind, event.Tool = managed.RoleUser, managed.KindToolResult, "exec"
		event.Content = Truncate(codexCommandResult(parsed))
	case "token_count":
		event.Role, event.Kind = managed.RoleSystem, managed.EventKind("usage")
		total := (*codexTokens)(nil)
		if parsed.Info != nil {
			total = parsed.Info.Total
		}
		usage := total.usage(model)
		event.Usage = &usage
	case "task_complete", "turn_complete", "task_failed":
		event.Role, event.Kind = managed.RoleSystem, KindResult
		event.Content = Truncate(firstNonEmpty(parsed.Message, parsed.Text, parsed.Reason))
	case "task_started", "turn_started", "session_configured":
		event.Role, event.Kind = managed.RoleSystem, managed.EventKind("system")
		event.Content = Truncate(kind)
	default:
		return raw(), nil
	}
	return []managed.Event{event}, nil
}

// codexCommandResult renders an executed command's outcome, keeping the exit
// code visible: a tool result that shows only stdout hides the failure.
func codexCommandResult(parsed codexEvent) string {
	var b strings.Builder
	if parsed.ExitCode != nil {
		fmt.Fprintf(&b, "exit %d\n", *parsed.ExitCode)
	}
	b.WriteString(parsed.Stdout)
	if strings.TrimSpace(parsed.Stderr) != "" {
		b.WriteString("\n")
		b.WriteString(parsed.Stderr)
	}
	return strings.TrimSpace(b.String())
}

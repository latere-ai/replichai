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

// ClaudeCodeHarness drives the Claude Code CLI. It speaks the Anthropic
// dialect, carries the worker's role in a system-prompt flag rather than a file
// in the workspace, and emits one JSON object per line.
type ClaudeCodeHarness struct{}

// NewClaudeCode returns the Claude Code adapter.
func NewClaudeCode() ClaudeCodeHarness { return ClaudeCodeHarness{} }

// Name is the canonical harness name.
func (ClaudeCodeHarness) Name() Name { return ClaudeCode }

// Binary is the executable a preflight looks for.
func (ClaudeCodeHarness) Binary() string { return "claude" }

// Dialects is the Anthropic wire protocol, and only that: a gateway speaking
// an OpenAI dialect is refused by the catalogue rather than discovered at the
// first request.
func (ClaudeCodeHarness) Dialects() []Dialect { return []Dialect{DialectAnthropic} }

// VersionArgv prints the installed version, which a run records.
func (ClaudeCodeHarness) VersionArgv() []string { return []string{"claude", "--version"} }

// Argv is the stage's command. The permission mode is bypassed deliberately:
// the sandbox is the boundary, and an interactive prompt inside a
// non-interactive run only stalls it. The runtime refuses to build this argv
// without a driver confining the stage.
func (ClaudeCodeHarness) Argv(stage Stage, request Request) ([]string, error) {
	workdir, task, err := stageWork(stage, request)
	if err != nil {
		return nil, err
	}
	argv := []string{
		"claude", "--print",
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions",
		"--add-dir", workdir,
	}
	if request.Model.ID != "" {
		argv = append(argv, "--model", request.Model.ID)
	}
	if strings.TrimSpace(request.Instructions) != "" {
		argv = append(argv, "--append-system-prompt", request.Instructions)
	}
	return append(argv, "--", task), nil
}

// Env is the harness's environment. A credential is named in the second
// return so the driver delivers it out of the process table; where the gateway
// authenticates through the harness's own login there is no credential at all,
// which is the case the host sandbox exists to serve.
func (ClaudeCodeHarness) Env(_ Stage, request Request) (map[string]string, []string, error) {
	// Claude Code puts its scratch area beside its temporary root — a
	// directory per working directory, a marker file per shell — and the
	// sandbox overrides TMPDIR to a shared path a session cannot write. So
	// the root is pointed inside the session, where the siblings it derives
	// land in a directory that is already writable and goes away with the
	// run. Without this every shell command fails before it runs.
	env := map[string]string{
		"CLAUDE_CODE_TMPDIR": request.Paths.HarnessTmp,
		// A reproduction does not phone home. Claude Code otherwise reaches a
		// telemetry service and an error reporter on every run; these turn
		// both off, and the egress allowlist no longer opens either host.
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"DISABLE_TELEMETRY":                        "1",
	}
	// The harness's own login lives in its own configuration directory.
	// Redirecting the directory is how a session gets an isolated one — and
	// it is also how a session loses the login, because an empty directory
	// holds no credential and the harness answers "not logged in". So the
	// redirect happens only where a credential is being supplied instead.
	if request.Gateway.Auth != AuthHarness {
		env["CLAUDE_CONFIG_DIR"] = request.Paths.Home
	}
	switch request.Gateway.Auth {
	case AuthHarness, AuthNone:
		return env, nil, nil
	case AuthAPIKey:
		if strings.TrimSpace(request.Gateway.Key) == "" {
			return nil, nil, fmt.Errorf("gateway %q declares an api key and has none", request.Gateway.Name)
		}
		if request.Gateway.BaseURL == "" {
			env["ANTHROPIC_API_KEY"] = request.Gateway.Key
			return env, []string{"ANTHROPIC_API_KEY"}, nil
		}
		env["ANTHROPIC_BASE_URL"] = request.Gateway.BaseURL
		env["ANTHROPIC_AUTH_TOKEN"] = request.Gateway.Key
		return env, []string{"ANTHROPIC_AUTH_TOKEN"}, nil
	default:
		return nil, nil, fmt.Errorf("unknown auth kind %q", request.Gateway.Auth)
	}
}

// Files writes the harness's own settings into its session-scoped
// configuration directory. Nothing goes into the workspace: a file written
// there is committed by the agent and published as part of the reproduction.
func (ClaudeCodeHarness) Files(_ Stage, request Request) ([]File, error) {
	// Where the login is the operator's own, so is the configuration: the
	// session writes none, because nothing would read it.
	if request.Gateway.Auth == AuthHarness {
		return nil, nil
	}
	settings, err := json.MarshalIndent(map[string]any{
		"includeCoAuthoredBy": false,
		"cleanupPeriodDays":   0,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return []File{{Path: filepath.Join(request.Paths.Home, "settings.json"), Mode: 0o600, Body: settings}}, nil
}

// Needs is what Claude Code requires of the machine.
//
// The login: its configuration directory and file, where the credential lives
// on Linux and the account state lives everywhere; and on macOS the login
// Keychain as well, because there the credential is in the keychain rather
// than in a file. All read-only, and only for a session that actually uses
// that sign-in. It is a real widening and the narrowest one that works;
// without it the harness answers "not logged in".
//
// The denials: the operator's standing instructions are not the method, and a
// reproduction that silently inherits them is not reproducing the paper. Their
// plugins, agents, commands and skills are not the method either, Claude Code
// loads all four at startup, and they run code.
func (ClaudeCodeHarness) Needs(machine Machine) Needs {
	needs := Needs{}
	// Nothing under the shared temporary root: the scratch area is
	// redirected into the session by CLAUDE_CODE_TMPDIR instead, so it is
	// writable without granting anything outside the run and it is removed
	// with the run.
	if machine.Home == "" {
		return needs
	}
	config := filepath.Join(machine.Home, ".claude")
	needs.Login = []Grant{{Path: config}, {Path: config + ".json"}}
	if machine.Platform == "darwin" {
		needs.Login = append(needs.Login, Grant{Path: filepath.Join(machine.Home, "Library", "Keychains")})
	}
	for _, name := range []string{"CLAUDE.md", "settings.json", "plugins", "agents", "commands", "skills"} {
		needs.Denies = append(needs.Denies, filepath.Join(config, name))
	}
	return needs
}

// claudeLine is the subset of the stream this adapter reads. Everything else
// survives in the event's Raw payload, which is the transcript's evidence.
type claudeLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Model   string `json:"model"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Cost    float64
	Message struct {
		Model   string `json:"model"`
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
			Content  json.RawMessage `json:"content"`
			IsError  bool            `json:"is_error"`
		} `json:"content"`
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
	Usage *claudeUsage `json:"usage"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreation            *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// usage maps the harness's own accounting onto the contract's buckets, which
// are mutually exclusive so each is priced exactly once. The split cache-write
// figures win where the harness reports them, because adding them to the
// unsplit total would count the same tokens twice.
func (u *claudeUsage) usage(model managed.ModelRef) managed.Usage {
	if u == nil {
		return managed.Usage{Model: model}
	}
	usage := managed.Usage{
		Model:       model,
		InputFresh:  u.InputTokens,
		InputCached: u.CacheReadInputTokens,
		Output:      u.OutputTokens,
	}
	if u.CacheCreation != nil && (u.CacheCreation.Ephemeral5m > 0 || u.CacheCreation.Ephemeral1h > 0) {
		usage.CacheWrite5m = u.CacheCreation.Ephemeral5m
		usage.CacheWrite1h = u.CacheCreation.Ephemeral1h
		return usage
	}
	usage.CacheWrite = u.CacheCreationInputTokens
	return usage
}

// Decode reads one line of the stream. A line this build does not recognise
// becomes one raw event rather than nothing: a transcript that silently drops
// what it cannot parse is not evidence.
func (ClaudeCodeHarness) Decode(stream string, index int, line []byte) ([]managed.Event, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, nil
	}
	base := managed.Event{ID: EventID(stream, index), Time: time.Now().UTC(), Raw: Raw(line)}
	var parsed claudeLine
	if err := json.Unmarshal(line, &parsed); err != nil {
		// A line this build cannot parse becomes a raw event rather than an
		// error: a transcript that drops what it does not understand is not
		// evidence, and the harness's stream is not this program's to define.
		base.Role, base.Kind, base.Content = managed.RoleSystem, KindRaw, Truncate(trimmed)
		//nolint:nilerr // an unparsable line is evidence, not a failure
		return []managed.Event{base}, nil
	}
	// total_cost_usd is the harness's own figure and is decoded separately so
	// a malformed number cannot lose the whole result line.
	var cost struct {
		Total float64 `json:"total_cost_usd"`
	}
	_ = json.Unmarshal(line, &cost)
	parsed.Cost = cost.Total

	model := managed.ModelRef{Provider: "anthropic", ID: firstNonEmpty(parsed.Message.Model, parsed.Model)}
	switch parsed.Type {
	case "system":
		base.Role, base.Kind = managed.RoleSystem, managed.EventKind("system")
		base.Content = Truncate(strings.TrimSpace(parsed.Subtype + " " + parsed.Model))
		return []managed.Event{base}, nil
	case "assistant":
		return claudeContentEvents(base, parsed, model, managed.RoleAgent), nil
	case "user":
		return claudeContentEvents(base, parsed, model, managed.RoleUser), nil
	case "result":
		base.Role, base.Kind = managed.RoleSystem, KindResult
		base.Content = Truncate(parsed.Result)
		usage := parsed.Usage.usage(model)
		if parsed.Cost > 0 {
			usage.Details = map[string]int64{"vendor_cost_micro_usd": int64(parsed.Cost * 1e6)}
		}
		base.Usage = &usage
		return []managed.Event{base}, nil
	default:
		base.Role, base.Kind, base.Content = managed.RoleSystem, KindRaw, Truncate(trimmed)
		return []managed.Event{base}, nil
	}
}

// claudeContentEvents expands one message line into an event per content
// block, so a turn that both spoke and called a tool reads as two entries. The
// line's raw payload is attached to the first, which is the one that carries
// its usage.
func claudeContentEvents(base managed.Event, parsed claudeLine, model managed.ModelRef, role managed.Role) []managed.Event {
	events := make([]managed.Event, 0, len(parsed.Message.Content))
	for _, block := range parsed.Message.Content {
		event := base
		event.Role = role
		if len(events) > 0 {
			event.ID = fmt.Sprintf("%s.%d", base.ID, len(events))
			event.Raw = nil
		}
		switch block.Type {
		case "text":
			event.Kind, event.Content = managed.KindMessage, Truncate(block.Text)
		case "thinking":
			event.Kind, event.Content = managed.KindThinking, Truncate(block.Thinking)
		case "tool_use":
			event.Kind, event.Tool, event.Content = managed.KindToolUse, block.Name, Truncate(string(block.Input))
		case "tool_result":
			event.Kind, event.Content = managed.KindToolResult, Truncate(decodeToolResult(block.Content))
		default:
			event.Kind, event.Content = KindRaw, Truncate(block.Type)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		base.Role, base.Kind = role, KindRaw
		events = []managed.Event{base}
	}
	if parsed.Message.Usage != nil {
		usage := parsed.Message.Usage.usage(model)
		events[0].Usage = &usage
	}
	return events
}

// decodeToolResult renders a tool result, whose content is a string in the
// simple case and a list of blocks in the rest.
func decodeToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			parts = append(parts, block.Text)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// stageWork is the working directory and task for one stage, shared by every
// harness: the judge reads the evidence copy and writes only its verdict.
func stageWork(stage Stage, request Request) (string, string, error) {
	switch stage {
	case StageWorker, StageRevision:
		if request.Paths.Workspace == "" {
			return "", "", fmt.Errorf("stage %q has no workspace", stage)
		}
		return request.Paths.Workspace, request.Task, nil
	case StageJudge:
		if request.Paths.Evidence == "" || request.Paths.Grade == "" {
			return "", "", fmt.Errorf("judge stage needs an evidence and a grade path")
		}
		return request.Paths.Evidence, request.Task, nil
	default:
		return "", "", fmt.Errorf("unknown stage %q", stage)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

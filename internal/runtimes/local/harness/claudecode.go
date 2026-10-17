// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

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

// Dialects returns the Anthropic wire protocol only. The catalog refuses a
// gateway that speaks an OpenAI dialect rather than letting the mismatch
// appear at the first request.
func (ClaudeCodeHarness) Dialects() []Dialect { return []Dialect{DialectAnthropic} }

// VersionArgv prints the installed version, which a run records.
func (ClaudeCodeHarness) VersionArgv() []string { return []string{"claude", "--version"} }

// Argv returns the stage's command. The permission mode is bypassed
// deliberately: the sandbox is the boundary, and an interactive prompt inside
// a non-interactive run only stalls it. The runtime refuses to build this argv
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

// Env returns the harness's environment. A credential is named in the second
// return value so that the driver keeps it out of the process table. When the
// gateway authenticates through the harness's own login there is no credential
// at all, which is the case the host sandbox exists to serve.
func (ClaudeCodeHarness) Env(_ Stage, request Request) (map[string]string, []string, error) {
	// Claude Code puts its scratch area under its temporary root, with a
	// directory per working directory and a marker file per shell, and the
	// sandbox overrides TMPDIR to a shared path a session cannot write. The
	// root is therefore pointed inside the session, so the paths derived
	// from it are in a directory that is already writable and is removed
	// with the run. Without this every shell command fails before it runs.
	env := map[string]string{
		"CLAUDE_CODE_TMPDIR": request.Paths.HarnessTmp,
		// A reproduction sends no telemetry. Claude Code otherwise contacts a
		// telemetry service and an error reporter on every run. These
		// variables turn both off, and the egress allowlist no longer opens
		// either host.
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"DISABLE_TELEMETRY":                        "1",
	}
	// The harness's own login lives in its configuration directory.
	// Redirecting the directory gives a session an isolated configuration,
	// but it also loses the login, because an empty directory holds no
	// credential and the harness reports "not logged in". The redirect
	// therefore happens only when a credential is supplied instead.
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

// Files returns the harness's settings, to be written into its session-scoped
// configuration directory. Nothing goes into the workspace, because a file
// written there is committed by the agent and published as part of the
// reproduction.
func (ClaudeCodeHarness) Files(_ Stage, request Request) ([]File, error) {
	// When the login is the operator's own, so is the configuration. The
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

// Needs reports what Claude Code requires of the machine.
//
// The login is the account file, ~/.claude.json, and the credential: on macOS
// the login Keychain, elsewhere ~/.claude/.credentials.json. All of it is
// read-only, and it is granted only to a session that uses that login. The
// configuration directory around the credential is not granted, because it
// holds the operator's conversation history (projects/, history.jsonl). The
// configuration directory itself cannot be moved into the session: the
// Keychain entry is named after the default directory, so a moved one has no
// login.
//
// The denials cover the operator's standing instructions, which are not the
// method, and the operator's plugins, agents, commands and skills, which
// Claude Code loads at startup and which run code. They stay denied even
// though nothing grants their directory, so a later grant cannot reopen
// them.
func (ClaudeCodeHarness) Needs(machine Machine) Needs {
	needs := Needs{}
	// Nothing is granted under the shared temporary root. The scratch area
	// is redirected into the session by CLAUDE_CODE_TMPDIR instead, so it is
	// writable without granting anything outside the run, and it is removed
	// with the run.
	if machine.Home == "" {
		return needs
	}
	config := filepath.Join(machine.Home, ".claude")
	needs.Login = []Grant{{Path: config + ".json"}}
	if machine.Platform == "darwin" {
		needs.Login = append(needs.Login, Grant{Path: filepath.Join(machine.Home, "Library", "Keychains")})
	} else {
		needs.Login = append(needs.Login, Grant{Path: filepath.Join(config, ".credentials.json")})
	}
	for _, name := range []string{"CLAUDE.md", "settings.json", "plugins", "agents", "commands", "skills"} {
		needs.Denies = append(needs.Denies, filepath.Join(config, name))
	}
	return needs
}

// claudeLine is the subset of the stream this adapter reads. Everything else
// is preserved in the event's Raw payload, which is the transcript's evidence.
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
// are mutually exclusive so that each is priced exactly once. The split
// cache-write figures take precedence when the harness reports them, because
// adding them to the unsplit total would count the same tokens twice.
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

// Decode reads one line of the stream. A line this build does not recognize
// becomes one raw event rather than being dropped, because a transcript that
// silently dropped what it could not parse would not be evidence.
func (ClaudeCodeHarness) Decode(stream string, index int, line []byte) ([]managed.Event, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, nil
	}
	// The runtime sets Time from its own clock. The harness decodes and the
	// runtime timestamps, so a test can drive both deterministically.
	base := managed.Event{ID: EventID(stream, index), Raw: Raw(line)}
	var parsed claudeLine
	if err := json.Unmarshal(line, &parsed); err != nil {
		// A line this build cannot parse becomes a raw event rather than an
		// error. A transcript that dropped what it did not understand would
		// not be evidence, and the harness's stream format is not this
		// program's to define.
		base.Role, base.Kind, base.Content = managed.RoleSystem, KindRaw, Truncate(trimmed)
		//nolint:nilerr // an unparsable line is evidence, not a failure
		return []managed.Event{base}, nil
	}
	// total_cost_usd is the harness's own figure. It is decoded separately so
	// that a malformed number cannot lose the whole result line.
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
			usage.Details = map[string]int64{managed.DetailVendorCost: int64(parsed.Cost * 1e6)}
		}
		base.Usage = &usage
		return []managed.Event{base}, nil
	default:
		base.Role, base.Kind, base.Content = managed.RoleSystem, KindRaw, Truncate(trimmed)
		return []managed.Event{base}, nil
	}
}

// claudeContentEvents expands one message line into an event per content
// block, so that a turn that both produced text and called a tool reads as two
// entries. The line's raw payload is attached to the first event, which is
// also the one that carries its usage.
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
// simple case and a list of blocks otherwise.
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

// stageWork returns the working directory and task for one stage. It is
// shared by every harness. The judge reads the evidence copy and writes only
// its verdict.
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

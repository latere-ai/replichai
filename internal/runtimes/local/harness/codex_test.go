// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// The recorded stream decodes to the same events twice, and its accumulated
// usage equals the harness's own final totals.
func TestCodexDecodesItsOwnStream(t *testing.T) {
	h := NewCodex()
	first, usage, lines := decodeFixture(t, h, "testdata/codex.jsonl")
	second, _, _ := decodeFixture(t, h, "testdata/codex.jsonl")

	if len(first) != len(second) {
		t.Fatalf("two decodes disagree: %d, %d events", len(first), len(second))
	}
	ids := make(map[string]bool, len(first))
	for index := range first {
		if first[index].ID != second[index].ID {
			t.Fatalf("event %d id moved between reads", index)
		}
		if ids[first[index].ID] {
			t.Fatalf("duplicate event id %q", first[index].ID)
		}
		ids[first[index].ID] = true
	}
	// The fixture is a recorded run, so these are the numbers Codex itself
	// reported for it.
	if usage.InputFresh != 40212 || usage.InputCached != 30208 || usage.Output != 79 {
		t.Fatalf("usage = %+v", usage)
	}
	if len(usage.Extra) != 0 {
		t.Error("a non-token figure became a billable meter")
	}

	// Every line produced an event carrying its raw payload, including the
	// one that is not JSON and the event type this build has never seen.
	withRaw := 0
	for _, event := range first {
		if len(event.Raw) > 0 {
			withRaw++
		}
	}
	if withRaw != lines {
		t.Fatalf("%d lines produced %d raw-carrying events", lines, withRaw)
	}
	kinds := make([]managed.EventKind, 0, len(first))
	for _, event := range first {
		kinds = append(kinds, event.Kind)
	}
	// The kinds a real run produced: a thread and a turn opening, the
	// agent's messages, the command it ran and what came back, the turn's
	// result, and the two lines this build has no case for.
	for _, want := range []managed.EventKind{managed.KindMessage, managed.KindToolUse, managed.KindToolResult, KindResult, KindRaw} {
		if !slices.Contains(kinds, want) {
			t.Errorf("no event of kind %q in %v", want, kinds)
		}
	}
}

// Reasoning is mapped where a run emits it. The recorded fixture is a trivial
// task that produced none, so the mapping is asserted directly rather than
// left to whether a model happened to think out loud.
func TestCodexMapsReasoning(t *testing.T) {
	events, err := NewCodex().Decode("worker-0", 0,
		[]byte(`{"type":"item.completed","item":{"id":"item_9","type":"reasoning","text":"The paper is on arXiv."}}`))
	if err != nil || len(events) != 1 || events[0].Kind != managed.KindThinking {
		t.Fatalf("Decode = %+v, %v", events, err)
	}
	if events[0].Content != "The paper is on arXiv." {
		t.Errorf("content = %q", events[0].Content)
	}
}

// A started item and its completion are the same item; recording both would
// say the same thing twice. A command is the exception: what it was and what
// it returned are two different facts.
func TestCodexDoesNotDoubleRecordAnItem(t *testing.T) {
	started, err := NewCodex().Decode("worker-0", 0,
		[]byte(`{"type":"item.started","item":{"id":"item_0","type":"agent_message","text":"hello"}}`))
	if err != nil || len(started) != 0 {
		t.Fatalf("a started message was recorded before it completed: %+v", started)
	}
	command, err := NewCodex().Decode("worker-0", 1,
		[]byte(`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"ls","status":"in_progress"}}`))
	if err != nil || len(command) != 1 || command[0].Kind != managed.KindToolUse {
		t.Fatalf("a started command was not recorded: %+v", command)
	}
}

// A failing command shows its exit code. A tool result carrying only stdout
// hides the failure the reproduction turned on.
func TestCodexToolResultKeepsTheExitCode(t *testing.T) {
	events, err := NewCodex().Decode("worker-0", 0,
		[]byte(`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"false","aggregated_output":"boom","exit_code":2,"status":"completed"}}`))
	if err != nil || len(events) != 1 {
		t.Fatalf("Decode = %v, %v", events, err)
	}
	if !strings.Contains(events[0].Content, "exit 2") || !strings.Contains(events[0].Content, "boom") {
		t.Fatalf("content = %q", events[0].Content)
	}
}

// The provider block points at the operator's gateway and reads its credential
// from a fixed environment name, so the configuration file never holds one.
func TestCodexConfigCarriesTheGatewayNotTheKey(t *testing.T) {
	h := NewCodex()
	request := Request{
		Instructions: "ROLE", Task: "TASK",
		Model:   managed.ModelRef{Provider: "qwen", ID: "qwen3-coder-480b"},
		Gateway: Gateway{Name: "lux", BaseURL: "https://lux.latere.ai/v1", Dialect: DialectOpenAIResponses, Auth: AuthAPIKey, Key: "sk-secret"},
		Paths:   Paths{Workspace: "/s/workspace", Home: "/s/home", Evidence: "/s/evidence", Grade: "/s/grade"},
	}
	files, err := h.Files(StageWorker, request)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	var config string
	for _, file := range files {
		if !Outside(request.Paths.Workspace, file.Path) {
			t.Errorf("configuration written inside the workspace: %s", file.Path)
		}
		if strings.Contains(string(file.Body), "sk-secret") {
			t.Fatalf("the credential was written into %s", file.Path)
		}
		if strings.HasSuffix(file.Path, "config.toml") {
			config = string(file.Body)
		}
	}
	for _, want := range []string{`base_url = "https://lux.latere.ai/v1"`, `wire_api = "responses"`, `env_key = "REPLICHAI_MODEL_KEY"`} {
		if !strings.Contains(config, want) {
			t.Errorf("config missing %q:\n%s", want, config)
		}
	}
	request.Gateway.Dialect = DialectOpenAIChat
	files, _ = h.Files(StageWorker, request)
	if !strings.Contains(string(files[0].Body), `wire_api = "chat"`) {
		t.Errorf("the chat dialect did not reach the provider block:\n%s", files[0].Body)
	}

	env, secrets, err := h.Env(StageWorker, request)
	if err != nil || !slices.Contains(secrets, CodexKeyEnv) || env[CodexKeyEnv] != "sk-secret" {
		t.Fatalf("credential channel = %v, %v, %v", env, secrets, err)
	}
	if env["CODEX_HOME"] != "/s/home" {
		t.Errorf("codex home = %q", env["CODEX_HOME"])
	}
}

// Codex refuses a directory it has not been told to trust, and a session's
// workspace is a repository it has never seen. The sandbox is the boundary, so
// its own trust prompt is skipped rather than worked around.
func TestCodexSkipsItsOwnTrustCheck(t *testing.T) {
	argv, err := NewCodex().Argv(StageWorker, Request{
		Task: "reproduce it", Paths: Paths{Workspace: "/s/workspace", Home: "/s/home"},
	})
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	if !slices.Contains(argv, "--skip-git-repo-check") {
		t.Fatalf("argv = %v", argv)
	}
}

// Codex is the harness an open-weights model behind an OpenAI-compatible
// gateway pairs with, and it does not answer for the Anthropic dialect.
func TestCodexSpeaksTheOpenAIDialects(t *testing.T) {
	h := NewCodex()
	if !Speaks(h, DialectOpenAIResponses) || !Speaks(h, DialectOpenAIChat) || Speaks(h, DialectAnthropic) {
		t.Fatalf("dialects = %v", h.Dialects())
	}
}

// Codex keeps its token in its own configuration directory and writes its own
// state back there — its aliases, its app-server, its session records — so the
// login grant is that directory, writable. It is a login grant rather than a
// standing one: a session carrying its own credential has CODEX_HOME redirected
// into the session and never touches the operator's. Its standing instructions
// are withheld for the same reason every harness's are.
func TestCodexLoginProfile(t *testing.T) {
	login := NewCodex().Needs(Machine{Platform: "darwin", Home: "/Users/x", TempDir: "/tmp", UID: 501})
	if len(login.Login) != 1 || login.Login[0].Path != "/Users/x/.codex" || !login.Login[0].Write {
		t.Fatalf("login = %+v, want the configuration directory, writable", login.Login)
	}
	if len(login.Denies) != 1 || !strings.Contains(login.Denies[0], "AGENTS.md") {
		t.Errorf("denies = %v", login.Denies)
	}
	// Its scratch area is what every stage gets, whichever login it runs on;
	// the configuration directory is not in this list, or a session on an
	// API key would be handed the operator's token.
	if len(login.Writes) != 1 || !strings.Contains(login.Writes[0], "codex-501") {
		t.Errorf("standing writes = %v, want the scratch area alone", login.Writes)
	}
	// It does not read an injected certificate authority, so its own
	// endpoints are tunnelled rather than inspected — still allowlisted.
	if len(login.TLSPassthrough) == 0 {
		t.Error("a client that will not trust the sandbox's authority has no passthrough")
	}
}

// A role the agent never receives is not a role. Codex reads one from AGENTS.md
// under CODEX_HOME and has no flag for it, and a run on the operator's own
// login leaves CODEX_HOME to the operator, so Files writes nothing there.
// Three runs of the real method proved what that costs: with no standing
// instructions the agent wrote its reproduction into a subdirectory, committed
// nothing, and the graded work could not be published at all. So the
// instructions travel with the task instead.
func TestCodexCarriesItsRoleWhenNoFileCan(t *testing.T) {
	request := Request{
		Task:         "reproduce it",
		Instructions: "Commit your progress as you finish each part.",
		Paths:        Paths{Workspace: "/s/workspace", Home: "/s/home"},
		Gateway:      Gateway{Name: "harness-login", Auth: AuthHarness},
	}
	files, err := NewCodex().Files(StageWorker, request)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("the operator's own configuration directory was written to: %+v", files)
	}
	argv, err := NewCodex().Argv(StageWorker, request)
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	prompt := argv[len(argv)-1]
	if !strings.Contains(prompt, request.Instructions) {
		t.Fatalf("the agent was started with no standing instructions: %q", prompt)
	}
	if !strings.Contains(prompt, request.Task) {
		t.Fatalf("the task did not survive: %q", prompt)
	}

	// Where a file does carry them, it stays the only copy: the same role
	// twice in one session is a prompt the operator did not write.
	keyed := request
	keyed.Gateway = Gateway{Name: "lux", BaseURL: "https://lux.latere.ai/v1", Dialect: DialectOpenAIResponses, Auth: AuthAPIKey, Key: "sk-x"}
	argv, err = NewCodex().Argv(StageWorker, keyed)
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	if prompt := argv[len(argv)-1]; prompt != keyed.Task {
		t.Fatalf("the instructions were sent twice: %q", prompt)
	}
}

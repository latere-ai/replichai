// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// TestCodexDecodesItsOwnStream checks that the recorded stream decodes to the
// same events twice, and that its accumulated usage equals the harness's own
// final totals.
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
	// The fixture is a recorded run, so these are the numbers Codex reported
	// for it.
	if usage.InputFresh != 40212 || usage.InputCached != 30208 || usage.Output != 79 {
		t.Fatalf("usage = %+v", usage)
	}
	if len(usage.Extra) != 0 {
		t.Error("a non-token figure became a billable meter")
	}

	// Every line produced an event carrying its raw payload, including the
	// line that is not JSON and the event type this build does not know.
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
	// These are the kinds a real run produced: a thread and a turn starting,
	// the agent's messages, the command it ran and its output, the turn's
	// result, and the two lines this build has no case for.
	for _, want := range []managed.EventKind{managed.KindMessage, managed.KindToolUse, managed.KindToolResult, KindResult, KindRaw} {
		if !slices.Contains(kinds, want) {
			t.Errorf("no event of kind %q in %v", want, kinds)
		}
	}
}

// TestCodexMapsReasoning checks that reasoning is mapped when a run emits it.
// The recorded fixture is a trivial task that produced none, so the mapping is
// asserted directly rather than depending on whether a model happened to emit
// reasoning.
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

// TestCodexDoesNotDoubleRecordAnItem checks that a started item and its
// completion are recorded once, since they are the same item. A command is
// the exception: the command and its result are two different facts.
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

// TestCodexToolResultKeepsTheExitCode checks that a failing command shows its
// exit code. A tool result carrying only stdout would hide a failure the
// reproduction depended on.
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

// TestCodexConfigCarriesTheGatewayNotTheKey checks that the provider block
// points at the operator's gateway and reads its credential from a fixed
// environment variable, so that the configuration file never holds one.
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

// TestCodexSkipsItsOwnTrustCheck checks that Codex's trust prompt is skipped.
// Codex refuses a directory it has not been told to trust, and a session's
// workspace is a repository it has never seen. The sandbox is the boundary, so
// the prompt is skipped rather than worked around.
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

// TestCodexSpeaksTheOpenAIDialects checks that Codex speaks both OpenAI
// dialects, which makes it the harness to pair with an open-weights model
// behind an OpenAI-compatible gateway, and that it does not speak the
// Anthropic dialect.
func TestCodexSpeaksTheOpenAIDialects(t *testing.T) {
	h := NewCodex()
	if !Speaks(h, DialectOpenAIResponses) || !Speaks(h, DialectOpenAIChat) || Speaks(h, DialectAnthropic) {
		t.Fatalf("dialects = %v", h.Dialects())
	}
}

// TestCodexLoginProfile checks the login grant. Codex keeps its token in its
// own configuration directory and writes its own state there, including its
// aliases, its app-server and its session records, so the login grant is that
// directory, writable. It is a login grant rather than a standing one: a
// session that carries its own credential has CODEX_HOME redirected into the
// session and never touches the operator's directory. The operator's standing
// instructions are withheld for the same reason as for every harness.
func TestCodexLoginProfile(t *testing.T) {
	login := NewCodex().Needs(Machine{Platform: "darwin", Home: "/Users/x", TempDir: "/tmp", UID: 501})
	if len(login.Login) != 1 || login.Login[0].Path != "/Users/x/.codex" || !login.Login[0].Write {
		t.Fatalf("login = %+v, want the configuration directory, writable", login.Login)
	}
	if len(login.Denies) != 1 || !strings.Contains(login.Denies[0], "AGENTS.md") {
		t.Errorf("denies = %v", login.Denies)
	}
	// Every stage gets the scratch area, regardless of which login it runs
	// on. The configuration directory is not in this list, because a session
	// on an API key would otherwise be given the operator's token.
	if len(login.Writes) != 1 || !strings.Contains(login.Writes[0], "codex-501") {
		t.Errorf("standing writes = %v, want the scratch area alone", login.Writes)
	}
	// Codex does not read an injected certificate authority, so its own
	// endpoints are tunnelled rather than inspected. They are still subject
	// to the allowlist.
	if len(login.TLSPassthrough) == 0 {
		t.Error("a client that will not trust the sandbox's authority has no passthrough")
	}
}

// TestCodexCarriesItsRoleWhenNoFileCan checks that the role reaches the agent
// when no file can carry it. Codex reads a role from AGENTS.md under
// CODEX_HOME and has no flag for it, and a run on the operator's own login
// leaves CODEX_HOME to the operator, so Files writes nothing there. Three runs
// of the real method showed the cost: with no standing instructions the agent
// wrote its reproduction into a subdirectory, committed nothing, and the
// graded work could not be published. The instructions therefore travel with
// the task instead.
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

	// When a file does carry the instructions, it is the only copy. Sending
	// the same role twice in one session would be a prompt the operator did
	// not write.
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

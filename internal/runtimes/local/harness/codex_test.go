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
	if usage.InputFresh != 2700 || usage.InputCached != 1000 || usage.Output != 720 {
		t.Fatalf("usage = %+v", usage)
	}
	// Reasoning output is decomposition of the output bucket, not a bucket:
	// it is already counted in Output and pricing never iterates Details.
	if usage.Details["reasoning_output"] != 300 {
		t.Errorf("reasoning tokens = %d", usage.Details["reasoning_output"])
	}
	if usage.Extra["reasoning_output"] != 0 {
		t.Error("reasoning tokens became a billable meter")
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
	for _, want := range []managed.EventKind{managed.KindThinking, managed.KindMessage, managed.KindToolUse, managed.KindToolResult, KindResult, KindRaw} {
		if !slices.Contains(kinds, want) {
			t.Errorf("no event of kind %q in %v", want, kinds)
		}
	}
}

// A failing command shows its exit code. A tool result carrying only stdout
// hides the failure the reproduction turned on.
func TestCodexToolResultKeepsTheExitCode(t *testing.T) {
	events, err := NewCodex().Decode(StageWorker, 0,
		[]byte(`{"id":"1","msg":{"type":"exec_command_end","stdout":"out","stderr":"boom","exit_code":2}}`))
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

// Codex is the harness an open-weights model behind an OpenAI-compatible
// gateway pairs with, and it does not answer for the Anthropic dialect.
func TestCodexSpeaksTheOpenAIDialects(t *testing.T) {
	h := NewCodex()
	if !Speaks(h, DialectOpenAIResponses) || !Speaks(h, DialectOpenAIChat) || Speaks(h, DialectAnthropic) {
		t.Fatalf("dialects = %v", h.Dialects())
	}
}

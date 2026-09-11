// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// decodeFixture runs one recorded stream through a harness the way the pump
// does, returning the events and the accumulated usage.
func decodeFixture(t *testing.T, h Harness, path string) ([]managed.Event, managed.Usage, int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()
	var (
		events []managed.Event
		usage  managed.Usage
		lines  int
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for index := 0; scanner.Scan(); index++ {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		lines++
		decoded, err := h.Decode("worker-0", index, scanner.Bytes())
		if err != nil {
			t.Fatalf("decode line %d: %v", index, err)
		}
		for _, event := range decoded {
			if event.Usage != nil {
				usage = Fold(usage, *event.Usage)
			}
		}
		events = append(events, decoded...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return events, usage, lines
}

// The recorded stream decodes to the same events twice, and its accumulated
// usage equals the harness's own final totals, bucket by bucket.
func TestClaudeCodeDecodesItsOwnStream(t *testing.T) {
	h := NewClaudeCode()
	first, usage, lines := decodeFixture(t, h, "testdata/claude-code.jsonl")
	second, _, _ := decodeFixture(t, h, "testdata/claude-code.jsonl")

	if len(first) != len(second) {
		t.Fatalf("two decodes disagree: %d, %d events", len(first), len(second))
	}
	for index := range first {
		if first[index].ID != second[index].ID {
			t.Fatalf("event %d id moved between reads: %q, %q", index, first[index].ID, second[index].ID)
		}
	}
	ids := make(map[string]bool, len(first))
	for _, event := range first {
		if ids[event.ID] {
			t.Fatalf("duplicate event id %q", event.ID)
		}
		ids[event.ID] = true
	}

	// The result line's cumulative totals are authoritative, so the
	// accumulated usage is exactly what the harness itself reported.
	want := managed.Usage{
		Model:        managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
		InputFresh:   2700,
		InputCached:  1000,
		CacheWrite5m: 1400,
		CacheWrite1h: 400,
		Output:       720,
	}
	if usage.InputFresh != want.InputFresh || usage.InputCached != want.InputCached ||
		usage.CacheWrite5m != want.CacheWrite5m || usage.CacheWrite1h != want.CacheWrite1h ||
		usage.Output != want.Output {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
	// The unsplit bucket stays empty when the split figures are present:
	// adding both would count the same tokens twice.
	if usage.CacheWrite != 0 {
		t.Errorf("cache write double-counted: %+v", usage)
	}
	if usage.Model != want.Model {
		t.Errorf("model = %+v", usage.Model)
	}

	// Every input line produced at least one event carrying its raw payload,
	// including the one that is not JSON and the one this build has no case
	// for. A transcript that drops what it cannot parse is not evidence.
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

// The vendor's own cost figure is recorded as decomposition, never as a bucket
// the platform would price.
func TestClaudeCodeRecordsTheVendorCostAsDetail(t *testing.T) {
	events, _, _ := decodeFixture(t, NewClaudeCode(), "testdata/claude-code.jsonl")
	last := events[len(events)-1]
	if last.Kind != KindResult || last.Usage == nil {
		t.Fatalf("last event = %+v", last)
	}
	if got := last.Usage.Details["vendor_cost_micro_usd"]; got != 421100 {
		t.Errorf("vendor cost = %d", got)
	}
	if len(last.Usage.Extra) != 0 {
		t.Error("the vendor's cost became a billable meter")
	}
}

func TestClaudeCodeArgvCarriesTheRoleOutsideTheWorkspace(t *testing.T) {
	h := NewClaudeCode()
	request := Request{
		Instructions: "ROLE", Task: "TASK",
		Model:   managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
		Gateway: Gateway{Auth: AuthHarness, Dialect: DialectAnthropic},
		Paths:   Paths{Workspace: "/s/workspace", Home: "/s/home", Tmp: "/s/tmp", Evidence: "/s/evidence", Grade: "/s/grade"},
	}
	argv, err := h.Argv(StageWorker, request)
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--append-system-prompt ROLE", "--model claude-opus-5", "--add-dir /s/workspace", "-- TASK"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %v", want, argv)
		}
	}
	// The judge works from the read-only evidence copy, not the workspace.
	judge, err := h.Argv(StageJudge, request)
	if err != nil {
		t.Fatalf("judge Argv: %v", err)
	}
	if !strings.Contains(strings.Join(judge, " "), "--add-dir /s/evidence") {
		t.Errorf("judge does not read the evidence copy: %v", judge)
	}

	request.Gateway.Auth = AuthAPIKey
	request.Gateway.Key = "k"
	files, err := h.Files(StageWorker, request)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no configuration was written at all")
	}
	for _, file := range files {
		if !Outside(request.Paths.Workspace, file.Path) {
			t.Errorf("configuration written inside the workspace: %s", file.Path)
		}
	}
}

// A harness login needs no credential at all, which is the case the host
// sandbox exists to serve. A key is named so the driver keeps it out of the
// process table.
func TestClaudeCodeCredentialChannels(t *testing.T) {
	h := NewClaudeCode()
	paths := Paths{Workspace: "/s/workspace", Home: "/s/home", Tmp: "/s/tmp", HarnessTmp: "/s/tmp/harness"}

	env, secrets, err := h.Env(StageWorker, Request{Gateway: Gateway{Auth: AuthHarness}, Paths: paths})
	if err != nil || len(secrets) != 0 {
		t.Fatalf("harness login demanded a credential: %v, %v", secrets, err)
	}
	// The login lives in the harness's own configuration directory.
	// Redirecting it to a session-scoped one is how a run loses the login:
	// an empty directory holds no credential, and the harness answers "not
	// logged in" — which is the whole default path, gone.
	if _, redirected := env["CLAUDE_CONFIG_DIR"]; redirected {
		t.Errorf("a harness login was pointed at an empty configuration directory: %v", env)
	}
	files, err := h.Files(StageWorker, Request{Gateway: Gateway{Auth: AuthHarness}, Paths: paths})
	if err != nil || len(files) != 0 {
		t.Errorf("a harness login wrote configuration nothing reads: %v, %v", files, err)
	}

	env, _, err = h.Env(StageWorker, Request{Gateway: Gateway{Auth: AuthAPIKey, Key: "k"}, Paths: paths})
	if err != nil || env["CLAUDE_CONFIG_DIR"] != "/s/home" {
		t.Errorf("a supplied credential did not get an isolated configuration: %v", env)
	}

	env, secrets, err = h.Env(StageWorker, Request{Gateway: Gateway{Auth: AuthAPIKey, Key: "k"}, Paths: paths})
	if err != nil || !slices.Contains(secrets, "ANTHROPIC_API_KEY") || env["ANTHROPIC_API_KEY"] != "k" {
		t.Fatalf("vendor key = %v, %v, %v", env, secrets, err)
	}

	env, secrets, err = h.Env(StageWorker, Request{Gateway: Gateway{Auth: AuthAPIKey, Key: "k", BaseURL: "https://gw/v1"}, Paths: paths})
	if err != nil || !slices.Contains(secrets, "ANTHROPIC_AUTH_TOKEN") || env["ANTHROPIC_BASE_URL"] != "https://gw/v1" {
		t.Fatalf("gateway key = %v, %v, %v", env, secrets, err)
	}

	if _, _, err = h.Env(StageWorker, Request{Gateway: Gateway{Auth: AuthAPIKey, Name: "lux"}, Paths: paths}); err == nil {
		t.Fatal("a gateway declaring a key and holding none was accepted")
	}
}

// Non-essential traffic is switched off whichever login the run uses, so a
// reproduction does not phone the harness's telemetry or error reporter.
func TestClaudeCodeDisablesNonEssentialTraffic(t *testing.T) {
	paths := Paths{Workspace: "/s/workspace", Home: "/s/home", HarnessTmp: "/s/tmp/harness"}
	for _, auth := range []AuthKind{AuthHarness, AuthAPIKey} {
		env, _, err := NewClaudeCode().Env(StageWorker,
			Request{Gateway: Gateway{Auth: auth, Key: "k"}, Paths: paths})
		if err != nil {
			t.Fatalf("%s: Env: %v", auth, err)
		}
		if env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "1" || env["DISABLE_TELEMETRY"] != "1" {
			t.Errorf("%s: telemetry was not disabled: %v", auth, env)
		}
	}
}

// The operator's own sign-in is reachable, and their own instructions are not.
// A reproduction that silently inherits a machine's global CLAUDE.md is not
// reproducing the paper, and a plugin is code the method never asked for.
func TestClaudeCodesLoginProfile(t *testing.T) {
	login := NewClaudeCode().Needs(Machine{Platform: "darwin", Home: "/Users/x", TempDir: "/tmp", UID: 501})
	// The login is its configuration directory and file, and on macOS the
	// keychain as well, because the credential is not in a file there. All
	// of it read-only: nothing of the operator's account is written.
	want := []string{"/Users/x/.claude", "/Users/x/.claude.json", "/Users/x/Library/Keychains"}
	if got := LoginPaths(login); !slices.Equal(got, want) {
		t.Fatalf("macOS login = %v, want %v", got, want)
	}
	for _, grant := range login.Login {
		if grant.Write {
			t.Errorf("%s is writable: the login is read, never written", grant.Path)
		}
	}
	// Elsewhere the credential is a file inside the configuration directory,
	// so that is all that is opened.
	linux := LoginPaths(NewClaudeCode().Needs(Machine{Platform: "linux", Home: "/home/x"}))
	if !slices.Equal(linux, []string{"/home/x/.claude", "/home/x/.claude.json"}) {
		t.Errorf("linux login = %v", linux)
	}
	// Everything Claude Code loads at startup that is the operator's and
	// not the method is withheld from the directory that has to be readable
	// for the login.
	for _, want := range []string{"CLAUDE.md", "plugins", "settings.json", "agents", "commands", "skills"} {
		found := false
		for _, deny := range login.Denies {
			if strings.Contains(deny, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s reaches the run: %v", want, login.Denies)
		}
	}
	// Nothing outside the session is writable: the scratch area is
	// redirected into it by the environment instead of granted under the
	// shared temporary root.
	if len(login.Writes) != 0 {
		t.Errorf("the harness was granted a path outside the session: %v", login.Writes)
	}
	// With no home there is nothing to grant and nothing to withhold.
	if empty := NewClaudeCode().Needs(Machine{Platform: "darwin"}); len(empty.Login) != 0 || len(empty.Denies) != 0 {
		t.Errorf("a homeless machine granted %+v", empty)
	}
}

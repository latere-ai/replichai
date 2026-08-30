// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/provider"
)

// The credential shapes a run can plausibly put in front of itself are removed
// from anything published. The host sandbox grants the harness its own
// configuration by design, so an agent that reads its own credential file puts
// the contents in a tool result — which is committed as evidence into a
// repository that may later be made public.
func TestCredentialShapesAreRemoved(t *testing.T) {
	cases := []struct {
		name  string
		input string
		gone  string
	}{
		{"an anthropic key", "export ANTHROPIC_API_KEY=sk-ant-api03-AbCdEf0123456789xyz", "sk-ant-api03-AbCdEf0123456789xyz"},
		{"a github token", "remote set-url https://ghp_AbCdEf0123456789ABCDEF@github.com/x", "ghp_AbCdEf0123456789ABCDEF"},
		{"a fine-grained token", "github_pat_11ABCDEFG0abcdefghijklmnop", "github_pat_11ABCDEFG0abcdefghijklmnop"},
		{"a google token", "Authorization: Bearer ya29.a0AfB_byC1234567890abcdefg", "ya29.a0AfB_byC1234567890abcdefg"},
		{"an aws key id", "AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"a jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
			"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
		{"a harness credential file", `{"access_token":"oat_01ABCDEFGHIJKLMNOP","expires":1}`, "oat_01ABCDEFGHIJKLMNOP"},
		{"a refresh token", `{"refresh_token": "rt_9876543210abcdef"}`, "rt_9876543210abcdef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactSecrets(c.input)
			if strings.Contains(got, c.gone) {
				t.Fatalf("the credential survived: %q", got)
			}
			if !strings.Contains(got, redacted) {
				t.Fatalf("nothing was marked as removed: %q", got)
			}
		})
	}
	// The field name survives, so a reader can see what was removed rather
	// than only that something was.
	if got := RedactSecrets(`{"access_token":"oat_01ABCDEFGHIJKLMNOP"}`); !strings.Contains(got, "access_token") {
		t.Errorf("the field name was removed too: %q", got)
	}
	// Ordinary prose is untouched.
	for _, ordinary := range []string{
		"The paper reports p = .003 in Table 2.",
		"pip install pypdf requests",
		"",
	} {
		if got := RedactSecrets(ordinary); got != ordinary {
			t.Errorf("ordinary text was altered: %q -> %q", ordinary, got)
		}
	}
}

// The sweep runs over everything the transcript publishes: the machine stream,
// the narrative, and the raw payloads the events carry.
func TestThePublishedTranscriptIsSwept(t *testing.T) {
	secret := "sk-ant-api03-AbCdEf0123456789xyz"
	events := []provider.Event{
		{Role: provider.RoleAgent, Kind: provider.KindMessage, Content: "reading my configuration"},
		{
			Role: provider.RoleUser, Kind: provider.KindToolResult,
			Content: `{"access_token":"` + secret + `"}`,
			Raw:     json.RawMessage(`{"type":"user","text":"` + secret + `"}`),
		},
	}
	files, err := RenderTranscriptFiles(events)
	if err != nil {
		t.Fatalf("RenderTranscriptFiles: %v", err)
	}
	for name, body := range files {
		if strings.Contains(string(body), secret) {
			t.Fatalf("%s publishes the credential:\n%s", name, body)
		}
	}
	// A reader is told the transcript is not verbatim, and why.
	if index := string(files[TranscriptMD]); !strings.Contains(index, "Credential-shaped text was removed") {
		t.Fatalf("the index does not say anything was removed:\n%s", index)
	}
	// A transcript with nothing to remove carries no note.
	clean, err := RenderTranscriptFiles([]provider.Event{{Kind: provider.KindMessage, Content: "hello"}})
	if err != nil {
		t.Fatalf("RenderTranscriptFiles: %v", err)
	}
	if strings.Contains(string(clean[TranscriptMD]), "Credential-shaped text was removed") {
		t.Fatal("a clean transcript claims something was removed")
	}
}

// The caller's own events are not modified: they are evidence the caller may
// still be holding, and the sweep is about what gets published.
func TestTheSweepDoesNotMutateTheCallersEvents(t *testing.T) {
	secret := "ghp_AbCdEf0123456789ABCDEF"
	events := []provider.Event{{Kind: provider.KindToolResult, Content: secret}}
	if _, err := RenderTranscriptFiles(events); err != nil {
		t.Fatalf("RenderTranscriptFiles: %v", err)
	}
	if events[0].Content != secret {
		t.Fatalf("the caller's events were rewritten: %q", events[0].Content)
	}
}

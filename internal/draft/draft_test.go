// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package draft

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// AC1: the OpenAI-compatible implementation is exercised against a server
// speaking that protocol. It is the implementation that makes switching to
// another model a configuration change rather than a code change, so it is
// the one tested against a fake server.
func TestOpenAICompatibleModel(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("authorization")
		_ = json.UnmarshalRead(r.Body, &gotBody)
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"  Two papers on trust in automation.  "}}],
			"usage":{"prompt_tokens":900,"completion_tokens":80,"prompt_tokens_details":{"cached_tokens":400}}
		}`))
	}))
	defer srv.Close()

	m, err := New(Config{Provider: "openai", BaseURL: srv.URL, Model: "local-small"}, "sk-test", srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := m.Complete(context.Background(), Request{System: "be brief", Prompt: "a project", MaxTokens: 200})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if got.Text != "Two papers on trust in automation." {
		t.Errorf("text = %q, want it trimmed", got.Text)
	}
	if got.Model != "local-small" || m.Name() != "local-small" {
		t.Errorf("model = %q", got.Model)
	}
	// This protocol reports cached tokens inside the prompt total, so they are
	// subtracted before pricing. Otherwise a cached prompt would be charged at
	// both the input rate and the cache rate.
	if got.Usage.InputTokens != 500 || got.Usage.CachedInputTokens != 400 || got.Usage.OutputTokens != 80 {
		t.Errorf("usage = %+v, want 500 fresh / 400 cached / 80 out", got.Usage)
	}
	// The system prompt is sent as its own message. That is what the protocol
	// expects, and a local runner ignores a system prompt sent any other way.
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", gotBody["messages"])
	}
	if first, _ := msgs[0].(map[string]any); first["role"] != "system" {
		t.Errorf("first message = %v, want the system prompt", msgs[0])
	}
}

// The Anthropic implementation speaks the Messages API, with the key in the
// header that API expects. A wrong header name would send the deployment's
// credential in a header the API does not read and fail as "unauthorized".
func TestAnthropicModel(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey, gotVersion = r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		_, _ = w.Write([]byte(`{
			"content":[{"type":"thinking","text":"hmm"},{"type":"text","text":"A paragraph."}],
			"usage":{"input_tokens":700,"output_tokens":60,"cache_read_input_tokens":100}
		}`))
	}))
	defer srv.Close()

	m, err := New(Config{Provider: "anthropic", BaseURL: srv.URL, Model: "claude-haiku-4-5"}, "sk-ant", srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := m.Complete(context.Background(), Request{Prompt: "a project", MaxTokens: 200})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotPath != "/v1/messages" || gotKey != "sk-ant" || gotVersion == "" {
		t.Errorf("request = %s key=%q version=%q", gotPath, gotKey, gotVersion)
	}
	// Only the text blocks form the answer; a thinking block is not text.
	if got.Text != "A paragraph." {
		t.Errorf("text = %q", got.Text)
	}
	if got.Usage.InputTokens != 700 || got.Usage.CachedInputTokens != 100 || got.Usage.OutputTokens != 60 {
		t.Errorf("usage = %+v", got.Usage)
	}
}

// An error from the model quotes the endpoint's response body, because an
// operator debugging a misconfigured endpoint needs the endpoint's own words.
func TestAnErrorQuotesTheEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	m, _ := New(Config{Provider: "openai", BaseURL: srv.URL, Model: "m"}, "bad", srv.Client())
	_, err := m.Complete(context.Background(), Request{Prompt: "x", MaxTokens: 10})
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error = %v, want it to quote the endpoint", err)
	}
}

// A deployment with no model configured and a deployment with a model but no
// key get different errors: the first does not draft, and the second is an
// operator's mistake.
func TestConfigurationRefusals(t *testing.T) {
	if _, err := New(Config{}, "k", nil); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no config = %v, want ErrNotConfigured", err)
	}
	if _, err := New(Config{Provider: "openai", Model: "m"}, "  ", nil); !errors.Is(err, ErrNoCredential) {
		t.Errorf("no key = %v, want ErrNoCredential", err)
	}
	if _, err := New(Config{Provider: "haiku-by-post", Model: "m"}, "k", nil); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("unknown provider = %v, want ErrNotConfigured", err)
	}
	if (Config{Provider: "openai"}).Configured() {
		t.Error("a provider with no model is not configured")
	}
}

// The prompt holds the project and its papers, and nothing else: no PDF, no
// transcript, no verdict.
func TestDescriptionPrompt(t *testing.T) {
	got := DescriptionPrompt("Trust in automation", []Paper{
		{Title: "Effects of Segmentation Overlays", Authors: []string{"Ada Lovelace", "Grace Hopper"}, Venue: "CHI '24", Year: 2024},
		{Ref: "https://example.edu/two.pdf"},
	})
	for _, want := range []string{"Trust in automation", "Effects of Segmentation Overlays", "Ada Lovelace, Grace Hopper", "CHI '24", "(2024)", "https://example.edu/two.pdf"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
	// A paper with no record contributes its reference and no invented fields.
	if strings.Contains(got, "—  —") {
		t.Errorf("a paper with no record left empty separators:\n%s", got)
	}
	// An empty project still has a prompt, because it still has a title.
	if empty := DescriptionPrompt("Nothing yet", nil); !strings.Contains(empty, "no papers") {
		t.Errorf("an empty project's prompt = %q", empty)
	}
}

// The system prompt belongs to the platform. It forbids a verdict, because
// no run has been read.
func TestTheSystemPromptForbidsAVerdict(t *testing.T) {
	for _, want := range []string{"Never invent", "no run has been read", "Two or three sentences"} {
		if !strings.Contains(DescriptionSystem, want) {
			t.Errorf("the system prompt no longer says %q", want)
		}
	}
}

// Choosing Anthropic needs only a model id: the deployment already holds an
// Anthropic credential for the agent that runs its sessions, and the endpoint
// defaults to that vendor's own. Only pointing drafting at another vendor
// needs an endpoint and a key of its own.
func TestAnthropicNeedsNothingButAModelID(t *testing.T) {
	m, err := New(Config{Provider: "anthropic", Model: "claude-haiku-4-5"}, "the-agents-key", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.Name() != "claude-haiku-4-5" {
		t.Errorf("model = %q", m.Name())
	}
	// The endpoint defaults to the vendor's own.
	if got := m.(*anthropicModel).base; got != "https://api.anthropic.com" {
		t.Errorf("base = %q, want the vendor's own", got)
	}
	// The OpenAI-compatible endpoint has a default too, so a model id alone
	// is enough for both providers. That case does need a key, and New
	// refuses without one.
	o, err := New(Config{Provider: "openai", Model: "small-1"}, "sk-other", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := o.(*openAIModel).base; got != "https://api.openai.com/v1" {
		t.Errorf("base = %q", got)
	}
}

// A model that answers with nothing produces an error, not a blank paragraph.
// The error is returned with the usage, because the tokens were spent and the
// caller still has to bill for them.
func TestAnEmptyAnswerIsRefusedWithItsUsage(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		body     string
	}{
		{"openai has no choices", "openai", `{"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":400}}}`},
		{"openai answers with whitespace", "openai", `{"choices":[{"message":{"content":"  \n "}}],"usage":{"prompt_tokens":900,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":400}}}`},
		{"anthropic answers with whitespace", "anthropic", `{"content":[{"type":"text","text":"   "}],"usage":{"input_tokens":500,"output_tokens":0,"cache_read_input_tokens":400}}`},
		{"anthropic answers with no text block", "anthropic", `{"content":[{"type":"thinking","text":"hmm"}],"usage":{"input_tokens":500,"output_tokens":0,"cache_read_input_tokens":400}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			m, err := New(Config{Provider: c.provider, BaseURL: srv.URL, Model: "m"}, "k", srv.Client())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := m.Complete(context.Background(), Request{Prompt: "a project", MaxTokens: 200})
			if !errors.Is(err, ErrEmptyDraft) {
				t.Fatalf("error = %v, want ErrEmptyDraft", err)
			}
			if got.Text != "" {
				t.Errorf("text = %q, want nothing", got.Text)
			}
			// The usage is why a Result is returned with the error. If it were
			// dropped, the deployment would pay for tokens nobody is charged
			// for.
			if got.Usage.InputTokens != 500 || got.Usage.CachedInputTokens != 400 {
				t.Errorf("usage = %+v, want 500 fresh / 400 cached", got.Usage)
			}
			if got.Model != "m" {
				t.Errorf("model = %q", got.Model)
			}
		})
	}
}

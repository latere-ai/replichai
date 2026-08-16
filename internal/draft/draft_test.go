package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// AC1: the OpenAI-compatible implementation is exercised against a server
// speaking that protocol. It is the one that makes "another model" a setting
// rather than a code change, so it is the one worth a fake.
func TestOpenAICompatibleModel(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
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
	// Cached tokens are inside the prompt total in this protocol, so they are
	// taken out before pricing: counted twice, a cached prompt would be charged
	// at the input rate and the cache rate at once.
	if got.Usage.InputTokens != 500 || got.Usage.CachedInputTokens != 400 || got.Usage.OutputTokens != 80 {
		t.Errorf("usage = %+v, want 500 fresh / 400 cached / 80 out", got.Usage)
	}
	// The system prompt travels as its own message, which is what the protocol
	// expects and what a local runner will otherwise ignore.
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", gotBody["messages"])
	}
	if first, _ := msgs[0].(map[string]any); first["role"] != "system" {
		t.Errorf("first message = %v, want the system prompt", msgs[0])
	}
}

// The Anthropic implementation speaks the Messages API, with the key in the
// header that API expects — a mistake here would send the deployment's
// credential in a header nobody reads and fail as "unauthorized".
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
	// Only the text blocks are the answer; a thinking block is not prose.
	if got.Text != "A paragraph." {
		t.Errorf("text = %q", got.Text)
	}
	if got.Usage.InputTokens != 700 || got.Usage.CachedInputTokens != 100 || got.Usage.OutputTokens != 60 {
		t.Errorf("usage = %+v", got.Usage)
	}
}

// A model that answers with an error says what it said: an operator debugging a
// misconfigured endpoint needs the endpoint's own words.
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

// A deployment that has configured nothing, or has configured a model with no
// key, says which of the two it is: one is a deployment that simply does not
// draft, and the other is an operator's mistake.
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

// The prompt is the project and its papers, and nothing else — no PDF, no
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

// What the model is told a description is belongs to the platform: it forbids a
// verdict, because no run has been read.
func TestTheSystemPromptForbidsAVerdict(t *testing.T) {
	for _, want := range []string{"Never invent", "no run has been read", "Two or three sentences"} {
		if !strings.Contains(DescriptionSystem, want) {
			t.Errorf("the system prompt no longer says %q", want)
		}
	}
}

// Choosing Anthropic is a model id and nothing else: the deployment already
// holds an Anthropic credential for the agent that runs its sessions, and the
// endpoint is that vendor's own. Only pointing drafting at another vendor needs
// an endpoint and a key of its own.
func TestAnthropicNeedsNothingButAModelID(t *testing.T) {
	m, err := New(Config{Provider: "anthropic", Model: "claude-haiku-4-5"}, "the-agents-key", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.Name() != "claude-haiku-4-5" {
		t.Errorf("model = %q", m.Name())
	}
	// The vendor's own endpoint, not one somebody had to type.
	if got := m.(*anthropicModel).base; got != "https://api.anthropic.com" {
		t.Errorf("base = %q, want the vendor's own", got)
	}
	// An OpenAI-compatible endpoint defaults too, so "just a model id" is the
	// shape everywhere — but that is the case where a key has to be supplied,
	// which New already refuses without.
	o, err := New(Config{Provider: "openai", Model: "small-1"}, "sk-other", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := o.(*openAIModel).base; got != "https://api.openai.com/v1" {
		t.Errorf("base = %q", got)
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package draft makes one short model call: it sends a prompt and returns a
// paragraph and a token count (039).
//
// It does not implement provider.Provider. That interface manages sessions,
// with a workspace, a worker, a run that can be rejoined, artifacts, and a
// close, and every method on it assumes durable state on the far side.
// Drafting a paragraph about a project is a single request and response.
// Running it through the managed-agents machinery would cost tens of seconds
// and a session's worth of bookkeeping for about two hundred tokens.
//
// Two implementations ship: Anthropic's Messages API, and any endpoint that
// speaks the OpenAI chat-completions protocol, which includes a local
// llama.cpp, vLLM, Ollama, OpenAI, Groq, Together and OpenRouter. Switching to
// another model is therefore a configuration change rather than a code change.
package draft

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	xotel "latere.ai/x/pkg/otel"

	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/wirejson"
)

// Errors this package returns.
var (
	// ErrNotConfigured reports a deployment with no drafting model. It is not
	// a failure: a deployment without one offers no draft control, and
	// descriptions are typed by hand.
	ErrNotConfigured = errors.New("no drafting model is configured")
	// ErrNoCredential reports a configured model with no key in the
	// environment. It is an operator's mistake and is reported to an operator.
	ErrNoCredential = errors.New("the drafting model has no credential")
	// ErrEmptyDraft reports a model that answered with no text. The call is
	// still charged for, because the tokens were spent, and the error stops a
	// caller from storing a blank description.
	ErrEmptyDraft = errors.New("the model returned no text")
)

// Request is one drafting job: the system prompt that says what kind of
// writing is wanted, and the material to write from.
type Request struct {
	// System says what kind of writing this is. The caller sets it, not the
	// operator: the platform decides what a description is, and the operator
	// only decides which model writes it.
	System string
	// Prompt is the material to write from. For a project description it is
	// what the project already holds.
	Prompt string
	// MaxTokens bounds the answer. A description is a paragraph, and this
	// bound keeps the price at the fraction of a cent it is sold for.
	MaxTokens int
}

// Result is the text, the model that wrote it, and the tokens it cost. Usage
// is a provider.Usage so the charge is computed by the same function that
// prices a session, credit.Rates.Price, rather than by a second one that
// could drift from it.
type Result struct {
	Text  string
	Model string
	Usage provider.Usage
}

// Model is one single-turn completion. Implementations are stateless and safe
// for concurrent use.
type Model interface {
	// Complete answers a request, or fails. It never retries: a draft is
	// requested by a user pressing a button, and pressing it again is the
	// retry.
	//
	// A model that produced no text fails with ErrEmptyDraft, and the Result
	// returned with the error still carries the usage the attempt cost.
	Complete(ctx context.Context, r Request) (Result, error)
	// Name returns the model id the answer is priced under.
	Name() string
}

// Config is the operator's choice (006k) of protocol, endpoint, and model.
// The credential is never stored here. It comes from the deployment's
// environment, because a settings table is read by every admin, included in
// every backup, and printed by any debug endpoint that is later added.
type Config struct {
	// Provider is "anthropic" or "openai". It names the protocol to speak,
	// not the company: "openai" is any endpoint that speaks chat-completions.
	Provider string `json:"provider,omitempty"`
	// BaseURL is the endpoint's address. Empty means the provider's own
	// default.
	BaseURL string `json:"base_url,omitempty"`
	// Model is the model id, which is also the name it is priced under. It
	// must be an exact entry in the rate card, or the deployment refuses to
	// draft.
	Model string `json:"model,omitempty"`
}

// Configured reports whether an operator has named a model to draft with.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Provider) != "" && strings.TrimSpace(c.Model) != ""
}

// New builds the model an operator configured. The key belongs to the
// deployment; the composition root reads it from the environment and passes
// it in. This package never reads the environment itself, so a test cannot
// accidentally use a real key.
//
// Choosing Anthropic needs only a model id: the deployment already holds an
// Anthropic credential for the agent that runs its sessions, and a second
// credential for the same vendor would add configuration without adding
// security. Pointing drafting at another vendor needs a key of its own,
// because the deployment does not otherwise hold one.
func New(c Config, key string, client *http.Client) (Model, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrNoCredential
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second, Transport: xotel.Transport(nil)}
	}
	switch strings.ToLower(strings.TrimSpace(c.Provider)) {
	case "anthropic":
		return &anthropicModel{base: firstNonEmpty(c.BaseURL, "https://api.anthropic.com"), model: c.Model, key: key, client: client}, nil
	case "openai", "openai-compatible":
		return &openAIModel{base: firstNonEmpty(c.BaseURL, "https://api.openai.com/v1"), model: c.Model, key: key, client: client}, nil
	default:
		return nil, fmt.Errorf("%w: unknown provider %q", ErrNotConfigured, c.Provider)
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimRight(strings.TrimSpace(a), "/")
	}
	return b
}

// chatMessage is one turn of the conversation. Both protocols spell a message
// the same way, so one type serves both bodies.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicRequest is a Messages API call. The fields deliberately carry no
// omitempty: the API receives the same shape on every call, empty system
// prompt included, so a draft is reproducible from the model id and the
// prompt alone.
type anthropicRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system"`
	Messages  []chatMessage `json:"messages"`
}

// openAIRequest is a chat-completions call. It has no system field because
// that protocol carries the system prompt as the first message.
type openAIRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Messages  []chatMessage `json:"messages"`
}

// anthropicModel calls the Messages API directly rather than through the SDK.
// The call is one POST with four fields, and the SDK's beta session machinery
// is what this package exists to avoid.
type anthropicModel struct {
	base, model, key string
	client           *http.Client
}

// Name returns the model id the answer is priced under.
func (m *anthropicModel) Name() string { return m.model }

// Complete asks the Messages API for one paragraph.
func (m *anthropicModel) Complete(ctx context.Context, r Request) (Result, error) {
	// The body is a typed struct rather than a map[string]any so the wire
	// shape is declared in one place, and so a field of a type the encoder
	// cannot handle is visible here rather than hidden in a literal.
	body, err := json.Marshal(anthropicRequest{
		Model:     m.model,
		MaxTokens: r.MaxTokens,
		System:    r.System,
		Messages:  []chatMessage{{Role: "user", Content: r.Prompt}},
	}, wirejson.Outbound)
	if err != nil {
		return Result{}, fmt.Errorf("drafting model: encoding the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", m.key)
	req.Header.Set("anthropic-version", "2023-06-01")

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := m.do(req, &out); err != nil {
		return Result{}, err
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return answered(Result{
		Text:  strings.TrimSpace(text.String()),
		Model: m.model,
		Usage: provider.Usage{
			InputTokens:       out.Usage.InputTokens,
			OutputTokens:      out.Usage.OutputTokens,
			CachedInputTokens: out.Usage.CacheReadInputTokens,
			CacheWriteTokens:  out.Usage.CacheCreationInputTokens,
		},
	})
}

// openAIModel speaks chat-completions, the protocol every other vendor and
// every local runner implements.
type openAIModel struct {
	base, model, key string
	client           *http.Client
}

// Name returns the model id the answer is priced under.
func (m *openAIModel) Name() string { return m.model }

// Complete asks a chat-completions endpoint for one paragraph.
func (m *openAIModel) Complete(ctx context.Context, r Request) (Result, error) {
	msgs := make([]chatMessage, 0, 2)
	if strings.TrimSpace(r.System) != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: r.System})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: r.Prompt})
	// Typed for the same reason as the Anthropic body above.
	body, err := json.Marshal(openAIRequest{
		Model:     m.model,
		MaxTokens: r.MaxTokens,
		Messages:  msgs,
	}, wirejson.Outbound)
	if err != nil {
		return Result{}, fmt.Errorf("drafting model: encoding the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+m.key)

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := m.do(req, &out); err != nil {
		return Result{}, err
	}
	text := ""
	if len(out.Choices) > 0 {
		text = strings.TrimSpace(out.Choices[0].Message.Content)
	}
	// This protocol reports cached prompt tokens inside the prompt total,
	// unlike Anthropic's, so they are subtracted before pricing. Otherwise
	// they would be charged twice, once at the input rate and once at the
	// cache-read rate.
	input := max(out.Usage.PromptTokens-out.Usage.PromptTokensDetails.CachedTokens, 0)
	return answered(Result{
		Text:  text,
		Model: m.model,
		Usage: provider.Usage{
			InputTokens:       input,
			OutputTokens:      out.Usage.CompletionTokens,
			CachedInputTokens: out.Usage.PromptTokensDetails.CachedTokens,
		},
	})
}

// answered turns a completed call into what Complete promises: a result, or
// ErrEmptyDraft when the model produced no text. The result is returned with
// the error rather than discarded, because its usage is the only record of
// the tokens spent. A caller that cannot see them cannot charge for them, and
// the deployment pays instead.
func answered(r Result) (Result, error) {
	if strings.TrimSpace(r.Text) == "" {
		return r, ErrEmptyDraft
	}
	return r, nil
}

func (m *openAIModel) do(req *http.Request, out any) error { return roundtrip(m.client, req, out) }

func (m *anthropicModel) do(req *http.Request, out any) error { return roundtrip(m.client, req, out) }

// roundtrip sends the request and decodes the answer, turning a non-2xx status
// into an error that quotes the endpoint's response. The body read is bounded
// to one megabyte, because a misconfigured base URL can point at any server,
// including one that returns a very large HTML page.
func roundtrip(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("drafting model: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out, wirejson.Foreign); err != nil {
		return fmt.Errorf("drafting model: decoding the answer: %w", err)
	}
	return nil
}

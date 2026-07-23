package hosted

import (
	"context"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// promptModel runs an identifier-extraction prompt. The task is small and
// bounded — read a page, name one DOI or arXiv ID — so a fast model is the
// right default rather than the one an audit uses.
const promptModel = anthropic.ModelClaudeHaiku4_5

// promptMaxTokens caps the reply. An identifier is short; a runaway answer is a
// wrong one, and verification rejects it downstream regardless.
const promptMaxTokens = 256

// Prompter is a one-shot text completion over the backend — the seam an
// agent-backed page resolver uses to read a page and name a paper's identifier.
// It reuses this adapter's client and credential resolution, so the backend
// stays named only here. Its Complete method satisfies the resolver's Prompter
// interface structurally, without either package importing the other.
type Prompter struct {
	client    anthropic.Client
	model     anthropic.Model
	maxTokens int64
}

// NewPrompter builds a Prompter, sharing New's credential handling: the
// REPLICHAI_API_KEY env var, else the SDK's own resolution.
func NewPrompter(opts ...Option) (*Prompter, error) {
	p, err := New(opts...)
	if err != nil {
		return nil, err
	}
	return &Prompter{client: p.client, model: promptModel, maxTokens: promptMaxTokens}, nil
}

// Complete returns the model's text for one prompt.
func (p *Prompter) Complete(ctx context.Context, prompt string) (string, error) {
	msg, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if text := block.AsText(); text.Text != "" {
			b.WriteString(text.Text)
		}
	}
	return b.String(), nil
}

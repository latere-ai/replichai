package paper

import (
	"context"
	"fmt"
	"strings"
)

// Prompter is a single-shot text completion: the seam an agent-backed
// PageResolver uses. A production adapter wraps the provider layer or the SDK;
// a test fakes it. Keeping the seam this narrow means resolution never depends
// on how the completion is produced.
type Prompter interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// PrompterFunc adapts a function to the Prompter interface.
type PrompterFunc func(ctx context.Context, prompt string) (string, error)

// Complete calls f.
func (f PrompterFunc) Complete(ctx context.Context, prompt string) (string, error) {
	return f(ctx, prompt)
}

// defaultMaxBody caps how much of a page an AgentPageResolver shows the model.
const defaultMaxBody = 16000

// AgentPageResolver reads a page with an LLM to name the paper's identifier,
// for the messy sites where the citation_* meta standard is absent. Its answer
// is only a candidate — resolveURL verifies the identifier occurs in the page —
// so the model can upgrade a digest-only page to a real DOI but can never
// invent an identity or make one drift between runs.
//
// It consults the standard meta tags first, since an exact tag beats asking the
// model, and only prompts when they are missing.
type AgentPageResolver struct {
	Prompter Prompter
	// MaxBody caps the page bytes shown to the model; zero uses a default.
	MaxBody int
}

// Identify returns a candidate identifier and title for the page.
func (a AgentPageResolver) Identify(ctx context.Context, url string, body []byte) (string, string, error) {
	if cand, title, _ := (metaPageResolver{}).Identify(ctx, url, body); cand != "" {
		return cand, title, nil
	}
	title := metaContent(body, citationTitleRe)
	if title == "" {
		title = htmlTitle(body)
	}
	if a.Prompter == nil {
		return "", title, nil // no model wired: fall through to a content digest
	}

	max := a.MaxBody
	if max <= 0 {
		max = defaultMaxBody
	}
	snippet := string(body)
	if len(snippet) > max {
		snippet = snippet[:max]
	}
	out, err := a.Prompter.Complete(ctx, fmt.Sprintf(identifyPrompt, url, snippet))
	if err != nil {
		return "", title, err
	}
	return parseCandidate(out), title, nil
}

// identifyPrompt asks for exactly one identifier, or NONE, so the reply needs no
// parsing beyond trimming. Verification, not trust, guards correctness.
const identifyPrompt = `You are identifying a single academic paper from a web page.
Return the paper's own DOI if the page states one, else its arXiv ID, else the word NONE.
Return only the identifier or NONE, with no other text.

URL: %s

PAGE:
%s`

// parseCandidate normalises a model reply to a bare candidate, mapping an
// explicit NONE (or empty) to "".
func parseCandidate(out string) string {
	out = strings.TrimSpace(out)
	if out == "" || strings.EqualFold(out, "none") {
		return ""
	}
	// Take the first whitespace-separated token in case the model adds stray
	// words despite the instruction; verification rejects a wrong guess anyway.
	if fields := strings.Fields(out); len(fields) > 0 {
		return fields[0]
	}
	return out
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"regexp"
	"strings"

	"github.com/latere-ai/replichai/internal/provider"
)

// The redaction marker. It is deliberately visible: a reader of a transcript
// should be able to see that something was removed, and where.
const redacted = "[redacted credential]"

// credentialPatterns are the credential shapes this sweep removes from
// anything published. The list is what a run can plausibly put in front of
// itself, not an attempt at completeness — a pattern table cannot prove a
// transcript holds no secret, and this is the backstop rather than the control.
//
// The control is that a stage is given only the credentials it needs. The
// backstop exists because one of them is readable by design: the host sandbox
// grants read on the harness's own configuration directory, which is what makes
// an existing login usable, and an agent that reads its own credential file
// puts the contents in a tool result — which becomes an event, which is
// committed as evidence into a repository that may later be made public.
var credentialPatterns = []*regexp.Regexp{
	// Anthropic and OpenAI-style API keys.
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	// GitHub tokens, classic and fine-grained.
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	// Google and AWS.
	regexp.MustCompile(`ya29\.[A-Za-z0-9._-]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// A JWT, which is the shape an OAuth access token in a harness's own
	// configuration file takes.
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// A credential named in JSON, which is how a harness stores its login:
	// the field name and the quoted value, keeping the name so a reader can
	// see what was removed.
	regexp.MustCompile(`("(?:access_token|refresh_token|api_?key|client_secret|password|secret)"\s*:\s*")[^"]{8,}(")`),
}

// RedactSecrets removes credential-shaped text from anything about to be
// published. It runs over the transcript rather than over the reproduction's
// own files, which the redistribution gate refuses by name; the transcript is
// the one published artifact whose content the system did not choose.
//
// It is lossy on purpose. A run that legitimately quotes a string shaped like a
// key loses it, and that is the right trade against publishing an operator's
// own credential in a repository they may make public.
func RedactSecrets(value string) string {
	if value == "" {
		return value
	}
	for _, pattern := range credentialPatterns {
		if pattern.NumSubexp() == 2 {
			value = pattern.ReplaceAllString(value, "${1}"+redacted+"${2}")
			continue
		}
		value = pattern.ReplaceAllString(value, redacted)
	}
	return value
}

// RedactedSomething reports whether a sweep changed anything, so a finalize can
// say so rather than leaving a reader to notice the marker.
func RedactedSomething(before, after string) bool { return before != after }

// redactEvents returns the events with every credential-shaped string removed
// from their content and their raw payload. The events themselves are copied:
// the caller's slice is evidence it may still be holding.
func redactEvents(events []provider.Event) ([]provider.Event, int) {
	out := make([]provider.Event, len(events))
	removed := 0
	for index, event := range events {
		out[index] = event
		if clean := RedactSecrets(event.Content); clean != event.Content {
			out[index].Content = clean
			removed++
		}
		if len(event.Raw) > 0 {
			if clean := RedactSecrets(string(event.Raw)); clean != string(event.Raw) {
				out[index].Raw = []byte(clean)
				removed++
			}
		}
	}
	return out, removed
}

// redactedNote is what TRANSCRIPT.md says when the sweep removed something, so
// a reader knows the transcript is not verbatim and why.
func redactedNote(count int) string {
	if count == 0 {
		return ""
	}
	return strings.TrimSpace(`
> Credential-shaped text was removed from this transcript before publication. ` +
		`Where you see ` + "`" + redacted + "`" + `, the run put something in front of itself that looked like a key.`)
}

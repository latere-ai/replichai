// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError is GitHub's answer to a request it refused: the status, the message
// the body carried and, on a rate limit, how long it asked the caller to wait.
// Every non-2xx answer becomes one of these at the one place responses are
// read, so a failure says what GitHub said rather than "unexpected status N".
type APIError struct {
	// Method and Path name the request, so the message stands on its own.
	Method string
	Path   string
	// Status is the HTTP status GitHub answered with.
	Status int
	// Message is the body's message, or the body itself where it was not
	// GitHub's JSON error shape.
	Message string
	// Details are the per-field messages GitHub attaches to a 422, which
	// carry the reason a validation failed ("name already exists on this
	// account").
	Details []string
	// wait is how long GitHub asked the caller to wait before retrying, on a
	// rate limit. Zero where it did not say.
	wait time.Duration
	// rateLimited says the refusal was a rate limit rather than a denial,
	// which is the difference between a retry and a mistake.
	rateLimited bool
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %d", e.Method, e.Path, e.Status)
	if text := http.StatusText(e.Status); text != "" {
		b.WriteString(" " + text)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if len(e.Details) > 0 {
		b.WriteString(" (" + strings.Join(e.Details, "; ") + ")")
	}
	if e.rateLimited && e.wait > 0 {
		fmt.Fprintf(&b, "; retry after %s", e.wait.Round(time.Second))
	}
	return b.String()
}

// Temporary reports whether the refusal was a rate limit, which a retry after
// RetryAfter can expect to pass. It is the structural shape managed.IsTemporary
// matches, so a caller that already backs off on a temporary runtime failure
// backs off on this one too without knowing this package.
func (e *APIError) Temporary() bool { return e.rateLimited }

// RetryAfter is how long GitHub asked the caller to wait before retrying, or
// zero where it gave no figure. A caller with zero picks its own back-off.
func (e *APIError) RetryAfter() time.Duration { return e.wait }

// saysAlreadyExists reports whether a 422 is GitHub refusing to create a
// repository whose name is taken, as opposed to any other validation failure
// — an invalid name, a blocked organisation, a plan limit — which all answer
// 422 as well and none of which means the repository is there to reuse.
func (e *APIError) saysAlreadyExists() bool {
	if e.Status != http.StatusUnprocessableEntity {
		return false
	}
	for _, text := range append([]string{e.Message}, e.Details...) {
		if strings.Contains(strings.ToLower(text), "already exists") {
			return true
		}
	}
	return false
}

// failure reads a refused response into an APIError. payload is the body,
// already read; now is the clock a rate-limit reset is measured against.
func failure(method, path string, resp *http.Response, payload []byte, now time.Time) *APIError {
	e := &APIError{Method: method, Path: path, Status: resp.StatusCode}
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Field   string `json:"field"`
		} `json:"errors"`
	}
	if json.Unmarshal(payload, &body) == nil && body.Message != "" {
		e.Message = body.Message
		for _, detail := range body.Errors {
			switch {
			case detail.Message != "":
				e.Details = append(e.Details, detail.Message)
			case detail.Code != "":
				e.Details = append(e.Details, detail.Field+" "+detail.Code)
			}
		}
	} else if text := strings.TrimSpace(string(payload)); text != "" {
		e.Message = truncate(text, 200)
	}
	e.rateLimited, e.wait = rateLimit(resp, now)
	return e
}

// rateLimit reads GitHub's two ways of saying "not now": a 429, or a 403 that
// carries Retry-After (a secondary limit) or X-RateLimit-Remaining: 0 (the
// primary one). The wait comes from Retry-After in seconds, else from the
// reset epoch; a rate limit with neither is temporary with no figure.
func rateLimit(resp *http.Response, now time.Time) (limited bool, wait time.Duration) {
	retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
	remaining := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))
	secondary := resp.StatusCode == http.StatusForbidden && (retryAfter != "" || remaining == "0")
	if resp.StatusCode != http.StatusTooManyRequests && !secondary {
		return false, 0
	}
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		return true, time.Duration(seconds) * time.Second
	}
	if reset, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")), 10, 64); err == nil {
		if until := time.Unix(reset, 0).Sub(now); until > 0 {
			return true, until
		}
	}
	return true, 0
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

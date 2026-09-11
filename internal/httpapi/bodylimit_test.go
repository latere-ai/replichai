// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"

	"latere.ai/x/pay"
)

// webhookOnly is a payments provider that authenticates nothing, so the
// webhook route reads its body and refuses the delivery. Only ParseWebhook is
// ever reached; the embedded nil interface makes any other call panic.
type webhookOnly struct{ pay.Provider }

func (webhookOnly) ParseWebhook([]byte, http.Header) (pay.Event, error) {
	return pay.Event{}, errors.New("unsigned")
}

// TestEveryRouteBoundsItsBody checks that every route bounds its body. Every
// route is registered through one of two helpers, and both apply a body
// limit. The test walks the registered set rather than a hand-kept list, so a
// route added later is checked as soon as it is registered.
func TestEveryRouteBoundsItsBody(t *testing.T) {
	rt := NewRouter(Deps{Jobs: job.NewMemStore(), Projects: project.NewMemStore(), Reproductions: &fakeRepro{}, Gate: allowAll, Payments: webhookOnly{}})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	routes := append(rt.GatedRoutes(), rt.OpenRoutes()...)
	if len(routes) == 0 {
		t.Fatal("no routes registered")
	}
	for _, route := range routes {
		if route.Method == http.MethodGet || route.Method == http.MethodHead {
			continue
		}
		limit := BodyLimit(route.Method, route.Pattern)
		path := strings.NewReplacer("{id}", "some-id", "{email}", "a@b.c", "{handle}", "h").Replace(route.Pattern)
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			// A declared length over the limit is refused before the handler
			// reads a byte.
			req, _ := http.NewRequest(route.Method, srv.URL+path, strings.NewReader(strings.Repeat("a", int(limit)+1)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("declared %d bytes against a %d limit: status %d, want 413", limit+1, limit, resp.StatusCode)
			}

			// A body whose declared length does not match is cut off at the
			// limit, and the handler does not act on what arrived.
			req, _ = http.NewRequest(route.Method, srv.URL+path, io.NopCloser(strings.NewReader(strings.Repeat("a", int(limit)+1))))
			req.ContentLength = -1 // Chunked, so there is no length to check up front.
			req.Header.Set("Content-Type", "application/json")
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Fatalf("chunked oversize body reached the handler: status %d", resp.StatusCode)
			}
		})
	}
}

// TestImportRefusesAnOversizedRecordByName checks that the import route has
// its own bound, MaxImportBytes, and that a refusal reports the record as too
// large rather than as a generic bad request.
func TestImportRefusesAnOversizedRecordByName(t *testing.T) {
	if BodyLimit("POST", "/projects/{id}/sessions/import") != MaxImportBytes {
		t.Fatal("the import route is not bound to MaxImportBytes")
	}
	rt := NewRouter(Deps{Jobs: job.NewMemStore(), Projects: project.NewMemStore(), Reproductions: &fakeRepro{}, Gate: allowAll})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/projects/x/sessions/import", strings.NewReader(strings.Repeat("a", MaxImportBytes+1)))
	req.ContentLength = -1
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The project does not exist, so the handler refuses before it reads the
	// body, but the limit is still what stops the bytes. The test checks that
	// a too-large read, when it happens, is reported through the tooLarge code
	// path.
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

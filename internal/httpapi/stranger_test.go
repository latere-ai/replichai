// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// TestAStrangerCannotTellAPrivateProjectFromNone checks that every
// project-scoped and session-scoped route resolves the project through one
// function, and that a private project the caller may not view answers exactly
// as a project that does not exist. The test walks the registered routes rather
// than a hand-kept list, so a route added later is checked as soon as it is
// registered. A route that reads its body before resolving the project would
// reveal the project through a 400, so the bodies here are valid JSON.
func TestAStrangerCannotTellAPrivateProjectFromNone(t *testing.T) {
	d := testDeps(allowAll)
	rt := NewRouter(d)
	h := rt.Handler()

	projectID := filed(t, d, "owner", "arxiv:2302.03714")
	w, started := startPaper(t, h, projectID, "arxiv:2302.03714", "owner")
	if w.Code != http.StatusAccepted && w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}

	replacer := strings.NewReplacer("{id}", projectID, "{email}", "x@example.com")
	jobReplacer := strings.NewReplacer("{id}", started.JobID)
	body := `{"email":"x@example.com","title":"t","url":"arxiv:2302.03714","paper_key":"arxiv:2302.03714","to":"` + projectID + `","amount":1,"reason":"r","attestation_version":"1"}`

	walked := 0
	for _, route := range append(rt.GatedRoutes(), rt.OpenRoutes()...) {
		var path string
		switch {
		case strings.HasPrefix(route.Pattern, "/projects/{id}"):
			path = replacer.Replace(route.Pattern)
		case strings.HasPrefix(route.Pattern, "/jobs/{id}") && started.JobID != "":
			path = jobReplacer.Replace(route.Pattern)
		default:
			continue
		}
		walked++
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			w := do(t, h, route.Method, path, body, "stranger")
			if w.Code != http.StatusNotFound {
				t.Fatalf("stranger got %d %s, want 404: this route tells a stranger the project exists", w.Code, w.Body.String())
			}
		})
	}
	if walked < 20 {
		t.Fatalf("walked only %d routes; the route table has changed shape", walked)
	}
}

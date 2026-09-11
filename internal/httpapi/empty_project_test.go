// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// These tests cover creating a project with no papers (036).
//
// The requirement they replace was left over from 006j, when creating a
// project also started work. 012 separated the two: a create records papers
// and enqueues nothing, and papers are added from inside the project
// afterwards. A paper required at creation time therefore guards nothing, and
// can be removed a moment later from the project it gated.
//
// What a project cannot do without is a name, because the title falls back to
// the first paper's reference. The refusal therefore moves rather than
// disappearing, and these three cases are the whole rule.

func createdID(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("create response is not JSON: %s", body)
	}
	return out.ProjectID
}

func TestAProjectMayBeCreatedWithNoPapers(t *testing.T) {
	h := NewRouter(testDeps(allowAll)).Handler()

	w := do(t, h, "POST", "/projects", `{"title":"Somewhere to put things"}`, "sub-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("create with no papers = %d %s, want 201", w.Code, w.Body)
	}
	id := createdID(t, w.Body.Bytes())
	if id == "" {
		t.Fatalf("create returned no project id: %s", w.Body)
	}

	// The project is readable, has the title it was given, and holds no papers.
	r := do(t, h, "GET", "/projects/"+id, "", "sub-1")
	if r.Code != http.StatusOK {
		t.Fatalf("reading the empty project = %d %s", r.Code, r.Body)
	}
	var detail struct {
		Project struct {
			Title string `json:"title"`
		} `json:"project"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatalf("project read is not JSON: %s", r.Body)
	}
	if detail.Project.Title != "Somewhere to put things" {
		t.Fatalf("the project is titled %q, want the one it was given", detail.Project.Title)
	}
	p := do(t, h, "GET", "/projects/"+id+"/papers", "", "sub-1")
	if p.Code != http.StatusOK {
		t.Fatalf("listing the papers of an empty project = %d %s", p.Code, p.Body)
	}
	var papers []map[string]any
	if err := json.Unmarshal(p.Body.Bytes(), &papers); err != nil {
		t.Fatalf("papers list is not JSON: %s", p.Body)
	}
	if len(papers) != 0 {
		t.Fatalf("a project created with no papers holds %d", len(papers))
	}
}

func TestAProjectWithNeitherTitleNorPaperIsRefused(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{}`, "sub-1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with neither = %d %s, want 400", w.Code, w.Body)
	}
	if got := errorCode(w.Body.Bytes()); got != "missing_title" {
		t.Fatalf("refused with %q, want missing_title — with no paper there is nothing to name it after", got)
	}
	// A refusal creates nothing. A create that partly succeeded would leave an
	// unnamed project the owner never asked for.
	list := do(t, h, "GET", "/projects", "", "sub-1")
	var out []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &out); err != nil {
		t.Fatalf("project list is not JSON: %s", list.Body)
	}
	if len(out) != 0 {
		t.Fatalf("the refused create still left %d projects", len(out))
	}
}

func TestAPaperStillNamesAnUntitledProject(t *testing.T) {
	h := NewRouter(testDeps(allowAll)).Handler()

	// This is the behaviour from 006j, unchanged: with a paper and no title,
	// the first reference is the placeholder name, which enrichment may later
	// replace.
	const ref = "https://arxiv.org/abs/2302.03714"
	w := do(t, h, "POST", "/projects", `{"papers":["`+ref+`"]}`, "sub-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("create with a paper and no title = %d %s", w.Code, w.Body)
	}
	r := do(t, h, "GET", "/projects/"+createdID(t, w.Body.Bytes()), "", "sub-1")
	var detail struct {
		Project struct {
			Title string `json:"title"`
		} `json:"project"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatalf("project read is not JSON: %s", r.Body)
	}
	if detail.Project.Title == "" {
		t.Fatal("an untitled project with a paper got no name at all")
	}
}

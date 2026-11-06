// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/project"
)

// projectOf reads a project back through the API a client uses.
func projectOf(t *testing.T, h http.Handler, id, sub string) project.Project {
	t.Helper()
	w := do(t, h, "GET", "/projects/"+id, "", sub)
	if w.Code != http.StatusOK {
		t.Fatalf("read project = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Project project.Project `json:"project"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return out.Project
}

// TestDescribingAProject covers AC1 and AC2: an owner describes their project,
// and the description reads back.
func TestDescribingAProject(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, err := d.Projects.Create(context.Background(), "me", "A project")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	const desc = "Two papers on whether a segmentation overlay changes what a driver trusts."
	w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":"`+desc+`"}`, "me")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body.String())
	}
	if got := projectOf(t, h, p.ID, "me"); got.Description != desc {
		t.Errorf("description = %q", got.Description)
	}
	// Renaming and describing are independent: one does not clear the other.
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"title":"Renamed"}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("rename = %d", w.Code)
	}
	got := projectOf(t, h, p.ID, "me")
	if got.Title != "Renamed" || got.Description != desc {
		t.Errorf("after a rename: title=%q description=%q", got.Title, got.Description)
	}
	// Clearing it is an edit like any other, and distinguishable from not
	// sending the field at all.
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":""}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("clear = %d", w.Code)
	}
	if got := projectOf(t, h, p.ID, "me"); got.Description != "" {
		t.Errorf("description after clearing = %q", got.Description)
	}
	// A body that changes nothing is refused.
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("an empty edit = %d, want 400", w.Code)
	}
}

// TestADescriptionIsBoundedAndOwned covers the AC1 refusals: a description
// that is too long, and a project the caller does not own.
func TestADescriptionIsBoundedAndOwned(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "A project")

	long, _ := json.Marshal(strings.Repeat("x", project.MaxDescription+1))
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":`+string(long)+`}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("an over-long description = %d, want 400", w.Code)
	}
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":"mine now"}`, "somebody-else"); w.Code != http.StatusNotFound {
		t.Errorf("a stranger describing a project = %d, want 404: a stranger learns nothing", w.Code)
	}
	if got := projectOf(t, h, p.ID, "me"); got.Description != "" {
		t.Errorf("a refused edit still wrote: %q", got.Description)
	}
}

// TestADescriptionIsAsVisibleAsItsProject covers AC3: the description has the
// project's visibility. A caller with no relation to a private project gets no
// description, by the same redaction that hides the owner, and gets it on a
// public one, because publishing discloses it.
func TestADescriptionIsAsVisibleAsItsProject(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "A project")
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":"what this is about"}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("describe = %d", w.Code)
	}

	// A private project is hidden from a caller with no relation to it, so
	// there is no project to read and no description to disclose.
	if w := do(t, h, "GET", "/projects/"+p.ID, "", "stranger"); w.Code != http.StatusNotFound {
		t.Errorf("a stranger reading a private project = %d, want 404", w.Code)
	}
	publishable(t, d, "me", "ada")
	if err := d.Projects.SetPublic(context.Background(), p.ID, project.SignOff{By: "me", Name: "Ada Lovelace", Contact: "ada@example.org"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Once public and read by somebody with no relation to it, the description
	// is disclosed with the rest of the publication, while the redaction still
	// strips the owner.
	got := projectOf(t, h, p.ID, "stranger")
	if got.Description != "what this is about" {
		t.Errorf("a public project's description = %q, want it readable", got.Description)
	}
	if got.Owner != "" {
		t.Errorf("the redaction stopped applying: owner = %q", got.Owner)
	}
}

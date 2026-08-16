package httpapi

import (
	"context"
	"encoding/json"
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

// AC1 and AC2: an owner describes their project, and it reads back.
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
	// A body that changes nothing is a mistake worth naming.
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("an empty edit = %d, want 400", w.Code)
	}
}

// AC1, the refusals: too long, and not yours.
func TestADescriptionIsBoundedAndOwned(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "A project")

	long, _ := json.Marshal(strings.Repeat("x", project.MaxDescription+1))
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":`+string(long)+`}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("an over-long description = %d, want 400", w.Code)
	}
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":"mine now"}`, "somebody-else"); w.Code != http.StatusForbidden {
		t.Errorf("a stranger describing a project = %d, want 403", w.Code)
	}
	if got := projectOf(t, h, p.ID, "me"); got.Description != "" {
		t.Errorf("a refused edit still wrote: %q", got.Description)
	}
}

// AC3: the description's visibility is the project's. A stranger reading a
// private project gets no description, by the same redaction that hides the
// owner — and gets it on a public one, because that is what publishing means.
func TestADescriptionIsAsVisibleAsItsProject(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "A project")
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"description":"what this is about"}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("describe = %d", w.Code)
	}

	// A private project is hidden from a stranger outright — there is no
	// description to leak because there is no project to read.
	if w := do(t, h, "GET", "/projects/"+p.ID, "", "stranger"); w.Code != http.StatusNotFound {
		t.Errorf("a stranger reading a private project = %d, want 404", w.Code)
	}
	publishable(t, d, "me", "ada")
	if err := d.Projects.SetPublic(context.Background(), p.ID, project.SignOff{By: "me", Name: "Ada Lovelace", Contact: "ada@example.org"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Public, and read by somebody with no stake in it: the description travels
	// with what publishing discloses, while the redaction still strips the owner.
	got := projectOf(t, h, p.ID, "stranger")
	if got.Description != "what this is about" {
		t.Errorf("a public project's description = %q, want it readable", got.Description)
	}
	if got.Owner != "" {
		t.Errorf("the redaction stopped applying: owner = %q", got.Owner)
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
)

// 043: a registry card says which paper it is a reproduction of. The reader it
// says it to has no account — the landing page's strip and /registry are read by
// strangers — so every assertion here drives GET /public with no principal at
// all, behind a gate that refuses everyone. A test written as the owner would
// pass while the landing page rendered a blank line.

// withPapers files papers on a project, straight through the store: what is
// being tested is the read, and adding a paper through the API is a gated write
// that would need an identity this test deliberately does not have.
func withPapers(t *testing.T, d Deps, projectID string, papers ...project.Paper) {
	t.Helper()
	for _, p := range papers {
		if err := d.Projects.AddPaper(context.Background(), projectID, p); err != nil {
			t.Fatalf("add paper: %v", err)
		}
	}
}

func TestThePublicListNamesEachPaper(t *testing.T) {
	d := pricedDeps(allowAll)
	h := NewRouter(d).Handler()
	pub, _ := publicProject(t, d, h, "owner")
	withPapers(t, d, pub.ID,
		project.Paper{Key: "doi:10.1145/1", Ref: "1", AddedBy: "owner", Title: "Point of no Undo",
			Record: paper.Record{
				Authors:  []string{"Ada Lovelace", "Grace Hopper"},
				Venue:    "CHI '24",
				Year:     2024,
				Abstract: "We measure attention over a long session, and report the decline.",
			}},
		project.Paper{Key: "doi:10.1145/2", Ref: "2", AddedBy: "owner", Title: "PinchCatcher"},
	)

	body := do(t, closedTo(d), "GET", "/public", "", "").Body.Bytes()
	var zone []ProjectView
	if err := json.Unmarshal(body, &zone); err != nil {
		t.Fatalf("public zone: %v", err)
	}
	if len(zone) != 1 {
		t.Fatalf("public zone = %d projects, want 1", len(zone))
	}
	got := zone[0].Papers
	if len(got) != 2 {
		t.Fatalf("the signed-out registry carries %d papers, want both of them: %+v", len(got), got)
	}
	// Added order, so "the first paper" means the same thing on the card as it
	// does on the project screen.
	if got[0].Title != "Point of no Undo" || got[1].Title != "PinchCatcher" {
		t.Errorf("paper order = %q, %q", got[0].Title, got[1].Title)
	}
	if len(got[0].Authors) != 2 || got[0].Authors[0] != "Ada Lovelace" || got[0].Venue != "CHI '24" || got[0].Year != 2024 {
		t.Errorf("the citation a stranger reads = %+v", got[0])
	}
	// A paper nobody could look up carries no citation, and says so by absence
	// rather than by a placeholder the card would have to filter out again.
	if len(got[1].Authors) != 0 || got[1].Venue != "" || got[1].Year != 0 {
		t.Errorf("a paper with no record arrived carrying one: %+v", got[1])
	}
	// The wire is the trimmed citation, not the record. An abstract is
	// paragraphs, and six cards must not ship six of them to print six lines.
	if strings.Contains(string(body), "abstract") || strings.Contains(string(body), "We measure attention") {
		t.Error("the public list ships a paper's abstract")
	}
	for _, field := range []string{"added_by", "edited", `"ref"`} {
		if strings.Contains(string(body), field) {
			t.Errorf("the public list ships %s, which is the owner screen's bookkeeping", field)
		}
	}
}

// The list reads every project's papers in one call. Without this the registry
// opens one store round trip per row on the route the internet calls
// unauthenticated, and nothing else in the response would look wrong.
func TestThePublicListReadsPapersOnce(t *testing.T) {
	d := pricedDeps(allowAll)
	h := NewRouter(d).Handler()
	for _, title := range []string{"one", "two", "three"} {
		p, _ := publicProject(t, d, h, "owner")
		withPapers(t, d, p.ID, project.Paper{Key: "doi:10.1145/" + title, Ref: title, AddedBy: "owner", Title: title})
	}

	counted := &countingProjects{ProjectStore: d.Projects}
	closed := d
	closed.Projects = counted
	closed.Gate = denyAll
	w := do(t, NewRouter(closed).Handler(), "GET", "/public", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /public = %d", w.Code)
	}
	if counted.perProject != 0 {
		t.Errorf("the registry read papers one project at a time %d times", counted.perProject)
	}
	if counted.batched != 1 {
		t.Errorf("the registry made %d batched paper reads, want exactly 1", counted.batched)
	}
}

// countingProjects records how a list reached the papers it rendered.
type countingProjects struct {
	ProjectStore
	perProject int
	batched    int
}

func (c *countingProjects) ListPapers(ctx context.Context, projectID string) ([]project.Paper, error) {
	c.perProject++
	return c.ProjectStore.ListPapers(ctx, projectID)
}

func (c *countingProjects) PapersByProjects(ctx context.Context, ids []string) (map[string][]project.Paper, error) {
	c.batched++
	return c.ProjectStore.PapersByProjects(ctx, ids)
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/paper"
)

// recordingResolver answers with a canned identity per reference and records
// what it was asked, so a test can assert both what was recorded and that the
// fill-in did not look up a paper that was already complete.
type recordingResolver struct {
	byRef map[string]ResolvedPaper
	asked []string
}

func (r *recordingResolver) Resolve(_ context.Context, ref string) (ResolvedPaper, error) {
	r.asked = append(r.asked, ref)
	got, ok := r.byRef[ref]
	if !ok {
		return ResolvedPaper{}, ErrUnresolvable
	}
	return got, nil
}

const theRef = "https://doi.org/10.1145/3772318.3791265"

func fullRecord() paper.Record {
	return paper.Record{
		Authors:   []string{"Ada Lovelace", "Grace Hopper"},
		Venue:     "CHI '24",
		Year:      2024,
		Volume:    "8",
		Issue:     "CSCW1",
		Pages:     "1-14",
		Publisher: "ACM",
	}
}

// papersOf reads a project's papers back through the API a client uses.
func papersOf(t *testing.T, h http.Handler, projectID, sub string) []PaperView {
	t.Helper()
	w := do(t, h, "GET", "/projects/"+projectID+"/papers", "", sub)
	if w.Code != http.StatusOK {
		t.Fatalf("list papers = %d: %s", w.Code, w.Body.String())
	}
	var out []PaperView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode papers: %v", err)
	}
	return out
}

// TestAddedPaperCarriesItsRecord checks AC4: a paper added to a project
// carries the record its source declared, and the record reaches the client
// that reads the project.
func TestAddedPaperCarriesItsRecord(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = &recordingResolver{byRef: map[string]ResolvedPaper{
		theRef: {Key: "doi:10.1145/3772318.3791265", Title: "A Real Paper", Scheme: "doi", Slug: "a-real-paper", Record: fullRecord()},
	}}
	rt := NewRouter(d)
	h := rt.Handler()
	_, id := createWithPaper(t, h, theRef, "sub")
	rt.WaitBackground() // The record is written after the response.

	papers := papersOf(t, h, id, "sub")
	if len(papers) != 1 {
		t.Fatalf("papers = %+v", papers)
	}
	got := papers[0]
	if got.Title != "A Real Paper" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Record.Venue != "CHI '24" || got.Record.Year != 2024 || len(got.Record.Authors) != 2 {
		t.Errorf("record = %+v, want the source's", got.Record)
	}
	if got.NeedsDetails() {
		t.Error("a paper with authors, venue and year does not need details")
	}
}

// TestCorrectingAPaper checks AC5: a member may correct a field, and a
// stranger may not.
func TestCorrectingAPaper(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = &recordingResolver{byRef: map[string]ResolvedPaper{
		theRef: {Key: "doi:10.1145/3772318.3791265", Title: "Mangled Titl", Scheme: "doi", Record: paper.Record{Authors: []string{"A. Lovelace"}, Venue: "CHI", Year: 2024}},
	}}
	rt := NewRouter(d)
	h := rt.Handler()
	_, id := createWithPaper(t, h, theRef, "sub")
	rt.WaitBackground() // The record is written after the response.
	key := papersOf(t, h, id, "sub")[0].Key

	body := `{"title":"Corrected Title","authors":["Ada Lovelace","Grace Hopper"],"venue":"  CHI '24  "}`
	w := do(t, h, "PATCH", "/projects/"+id+"/papers?key="+key, body, "sub")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body.String())
	}
	var got PaperView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "Corrected Title" || got.Record.Venue != "CHI '24" {
		t.Errorf("correction = %+v (venue should be trimmed)", got)
	}
	if len(got.Record.Authors) != 2 {
		t.Errorf("authors = %v", got.Record.Authors)
	}
	// Exactly the fields written are marked as edited, so the rest remain open
	// to enrichment.
	if !got.WasEdited("title") || !got.WasEdited("venue") || got.WasEdited("year") {
		t.Errorf("edited = %v", got.Edited)
	}

	if w := do(t, h, "PATCH", "/projects/"+id+"/papers?key="+key, body, "stranger"); w.Code == http.StatusOK {
		t.Error("a stranger corrected somebody else's project")
	}
	if w := do(t, h, "PATCH", "/projects/"+id+"/papers?key=doi:nobody", body, "sub"); w.Code != http.StatusNotFound {
		t.Errorf("correcting a paper the project does not hold = %d, want 404", w.Code)
	}
	if w := do(t, h, "PATCH", "/projects/"+id+"/papers", body, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("a correction with no key = %d, want 400", w.Code)
	}
}

// TestACorrectionIsBounded checks that a correction is validated: a
// bibliography field has a maximum length, and a year must be a plausible year.
func TestACorrectionIsBounded(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = &recordingResolver{byRef: map[string]ResolvedPaper{theRef: {Key: "doi:x", Title: "T"}}}
	rt := NewRouter(d)
	h := rt.Handler()
	_, id := createWithPaper(t, h, theRef, "sub")
	rt.WaitBackground() // The record is written after the response.
	key := papersOf(t, h, id, "sub")[0].Key
	long := `{"venue":"` + strings.Repeat("x", maxPaperField+1) + `"}`

	for _, tc := range []struct{ name, body string }{
		{"an over-long field", long},
		{"a year that is not one", `{"year":12}`},
	} {
		if w := do(t, h, "PATCH", "/projects/"+id+"/papers?key="+key, tc.body, "sub"); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", tc.name, w.Code)
		}
	}
	// An empty author row is dropped rather than stored as an author.
	w := do(t, h, "PATCH", "/projects/"+id+"/papers?key="+key, `{"authors":["Ada Lovelace","  "]}`, "sub")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body.String())
	}
	if got := papersOf(t, h, id, "sub")[0].Record.Authors; len(got) != 1 {
		t.Errorf("authors = %v, want the blank row dropped", got)
	}
}

// TestFillingInTheDetails checks AC6 and AC7: the fill-in completes what is
// incomplete, leaves corrections alone, and does nothing the second time.
func TestFillingInTheDetails(t *testing.T) {
	res := &recordingResolver{byRef: map[string]ResolvedPaper{
		// The source answers with a record. The project starts without one,
		// because it predates the bibliography.
		theRef: {Key: "doi:10.1145/3772318.3791265", Title: "A Real Paper", Scheme: "doi", Record: fullRecord()},
	}}
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	// The project is created with no resolver, so the paper has no record,
	// which is the state of every paper added before 037.
	_, id := createWithPaper(t, h, theRef, "sub")
	if got := papersOf(t, h, id, "sub")[0]; !got.NeedsDetails() {
		t.Fatalf("the fixture is not a bare paper: %+v", got)
	}

	// The owner corrects one field before asking for the rest to be filled in.
	key := papersOf(t, h, id, "sub")[0].Key
	if w := do(t, h, "PATCH", "/projects/"+id+"/papers?key="+key, `{"venue":"Proc. ACM HCI"}`, "sub"); w.Code != http.StatusOK {
		t.Fatalf("correct: %d", w.Code)
	}

	d.Resolver = res
	h = NewRouter(d).Handler()
	w := do(t, h, "POST", "/projects/"+id+"/papers/refresh", "", "sub")
	if w.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Filled int         `json:"filled"`
		Papers []PaperView `json:"papers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Filled != 1 || len(out.Papers) != 1 {
		t.Fatalf("filled=%d papers=%d, want 1 and 1", out.Filled, len(out.Papers))
	}
	got := out.Papers[0]
	if got.Record.Venue != "Proc. ACM HCI" {
		t.Errorf("the fill-in overwrote a correction: %q", got.Record.Venue)
	}
	if got.Record.Year != 2024 || len(got.Record.Authors) != 2 || got.Title != "A Real Paper" {
		t.Errorf("the fill-in did not complete the record: %+v", got)
	}

	// A second fill-in changes nothing and does not ask the source again for a
	// paper that is now complete.
	asked := len(res.asked)
	w = do(t, h, "POST", "/projects/"+id+"/papers/refresh", "", "sub")
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Filled != 0 {
		t.Errorf("a second fill-in changed %d papers", out.Filled)
	}
	if len(res.asked) != asked {
		t.Errorf("a complete paper was looked up again: %v", res.asked)
	}

	if w := do(t, h, "POST", "/projects/"+id+"/papers/refresh", "", "stranger"); w.Code == http.StatusOK {
		t.Error("a stranger filled in somebody else's project")
	}
}

// TestFillingInSurvivesAnUnreachableSource checks that a source that cannot
// answer leaves the paper as it was and the request still succeeds. The fill-in
// is best-effort, and a Crossref outage is not reported to the owner as an
// error.
func TestFillingInSurvivesAnUnreachableSource(t *testing.T) {
	d := testDeps(allowAll)
	rt := NewRouter(d)
	h := rt.Handler()
	_, id := createWithPaper(t, h, theRef, "sub")
	rt.WaitBackground() // The record is written after the response.

	d.Resolver = &recordingResolver{byRef: map[string]ResolvedPaper{}} // Answers nothing.
	h = NewRouter(d).Handler()
	w := do(t, h, "POST", "/projects/"+id+"/papers/refresh", "", "sub")
	if w.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", w.Code, w.Body.String())
	}
	if got := papersOf(t, h, id, "sub")[0]; !got.NeedsDetails() {
		t.Errorf("a paper was filled in from a source that answered nothing: %+v", got)
	}
}

// TestTheSweepWalksEveryProject checks AC8: the platform's own fill-in walks
// every project, including the ones nobody has opened, and is safe to run
// again.
func TestTheSweepWalksEveryProject(t *testing.T) {
	d := testDeps(allowAll)
	rt := NewRouter(d)
	h := rt.Handler()
	_, first := createWithPaper(t, h, theRef, "sub")
	_, second := createWithPaper(t, h, "https://arxiv.org/abs/2603.01795", "other")
	rt.WaitBackground()

	res := &recordingResolver{byRef: map[string]ResolvedPaper{
		theRef:                             {Key: "doi:10.1145/3772318.3791265", Title: "A Real Paper", Record: fullRecord()},
		"https://arxiv.org/abs/2603.01795": {Key: "arxiv:2603.01795", Title: "A Preprint", Record: paper.Record{Authors: []string{"Ada Lovelace"}, Venue: "arXiv", Year: 2026}},
	}}
	d.Resolver = res
	rt = NewRouter(d)
	h = rt.Handler()

	projects, filled, err := rt.SweepPapers(context.Background())
	if err != nil {
		t.Fatalf("SweepPapers: %v", err)
	}
	if projects != 2 || filled != 2 {
		t.Fatalf("sweep looked at %d projects and filled %d, want 2 and 2", projects, filled)
	}
	for who, id := range map[string]string{"sub": first, "other": second} {
		if got := papersOf(t, h, id, who)[0]; got.NeedsDetails() {
			t.Errorf("project %s was not filled in: %+v", id, got)
		}
	}

	// A second sweep changes nothing and asks no source.
	asked := len(res.asked)
	if _, filled, err := rt.SweepPapers(context.Background()); err != nil || filled != 0 {
		t.Errorf("a second sweep filled %d (err %v)", filled, err)
	}
	if len(res.asked) != asked {
		t.Errorf("the second sweep asked the sources again: %v", res.asked[asked:])
	}
}

// TestTheSweepNeedsAResolver checks that a deployment with no resolver returns
// an error rather than reporting a sweep that did nothing.
func TestTheSweepNeedsAResolver(t *testing.T) {
	rt := NewRouter(testDeps(allowAll))
	if _, _, err := rt.SweepPapers(context.Background()); err == nil {
		t.Error("a sweep with no resolver should say so")
	}
}

// TestAMovedPaperKeepsItsRecord checks AC9: a paper moved to another project
// (035) arrives with its record and its corrections intact, because it moves
// as a row and the record is on the row.
func TestAMovedPaperKeepsItsRecord(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = &recordingResolver{byRef: map[string]ResolvedPaper{
		theRef: {Key: "doi:10.1145/3772318.3791265", Title: "A Real Paper", Record: fullRecord()},
	}}
	rt := NewRouter(d)
	h := rt.Handler()
	_, from := createWithPaper(t, h, theRef, "sub")
	rt.WaitBackground()
	key := papersOf(t, h, from, "sub")[0].Key
	if w := do(t, h, "PATCH", "/projects/"+from+"/papers?key="+key, `{"venue":"Proc. ACM HCI"}`, "sub"); w.Code != http.StatusOK {
		t.Fatalf("correct: %d", w.Code)
	}

	w := do(t, h, "POST", "/projects", `{"title":"Somewhere Else"}`, "sub")
	if w.Code != http.StatusCreated {
		t.Fatalf("create destination = %d: %s", w.Code, w.Body.String())
	}
	var made struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatalf("decode: %v", err)
	}
	body := `{"paper_key":"` + key + `","to_project":"` + made.ProjectID + `"}`
	if w := do(t, h, "POST", "/projects/"+from+"/papers/move", body, "sub"); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("move = %d: %s", w.Code, w.Body.String())
	}
	got := papersOf(t, h, made.ProjectID, "sub")
	if len(got) != 1 {
		t.Fatalf("the destination holds %d papers", len(got))
	}
	if got[0].Record.Venue != "Proc. ACM HCI" || got[0].Record.Year != 2024 {
		t.Errorf("record did not travel: %+v", got[0].Record)
	}
	if !got[0].WasEdited("venue") {
		t.Errorf("the correction's mark did not travel: %v", got[0].Edited)
	}
}

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// filed makes a project holding one paper, which is what a session — imported
// or run here — must be filed against.
func filed(t *testing.T, d Deps, owner, key string) string {
	t.Helper()
	p, err := d.Projects.Create(context.Background(), owner, "Local work")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := d.Projects.AddPaper(context.Background(), p.ID, project.Paper{
		Key: key, Ref: "10.1145/3654777.3676417", Title: "A paper", AddedBy: owner,
	}); err != nil {
		t.Fatalf("AddPaper: %v", err)
	}
	return p.ID
}

const importedBody = `{
  "paper_key": "doi:10.1145/3654777.3676417",
  "runtime": "local", "session_id": "7f3a91c2", "harness": "claude-code", "sandbox": "host",
  "phase": "done", "grade": "passed",
  "model": {"provider": "anthropic", "id": "claude-opus-5"},
  "usage": {"input_fresh": 1000, "input_cached": 200, "output": 300},
  "repo": "open-hai/a-paper-xyz",
  "started_at": "2026-08-25T10:00:00Z", "finished_at": "2026-08-25T10:42:00Z"
}`

// A session run on somebody's machine lands as an ordinary session with an
// origin, and every read path renders it with no special case.
func TestAnImportedSessionIsAnOrdinarySession(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

	response := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", importedBody, "me")
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}
	var body struct {
		JobID  string `json:"job_id"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Origin != string(job.OriginLocal) {
		t.Fatalf("origin = %q", body.Origin)
	}
	stored, err := d.Jobs.Get(context.Background(), body.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Phase != job.PhaseDone || stored.RepoName != "open-hai/a-paper-xyz" {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.Status.Model != "claude-opus-5" || stored.Status.Usage.InputTokens != 1000 {
		t.Fatalf("status = %+v", stored.Status)
	}
	if stored.Status.Verdict == nil || !stored.Status.Verdict.Passed() {
		t.Fatalf("verdict = %+v", stored.Status.Verdict)
	}
	// The session reads back through the ordinary project listing, with no
	// handler asking what its origin is.
	listing := do(t, h, http.MethodGet, "/projects/"+id, "", "me")
	if listing.Code != http.StatusOK {
		t.Fatalf("project read = %d: %s", listing.Code, listing.Body)
	}
	if !contains(listing.Body.String(), body.JobID) {
		t.Fatalf("the imported session is not in the project:\n%s", listing.Body)
	}
}

// Re-pushing the same session updates one row, which is what makes an
// interrupted upload resumable by running it again.
func TestReimportingIsIdempotentOverHTTP(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

	first := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", importedBody, "me")
	second := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", importedBody, "me")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("codes = %d, %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("a re-push opened a second session:\n%s\n%s", first.Body, second.Body)
	}
	sessions, err := d.Jobs.ListByProject(context.Background(), id)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("%d sessions after two pushes", len(sessions))
	}
}

// The record must be about a paper the project holds, and about a session that
// has actually ended.
func TestTheImportIsRefusedWhenItIsNotARecord(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", "doi:10.1145/3654777.3676417")

	running := `{"paper_key":"doi:10.1145/3654777.3676417","runtime":"local","session_id":"x","phase":"running"}`
	if response := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", running, "me"); response.Code != http.StatusBadRequest {
		t.Fatalf("a running session = %d: %s", response.Code, response.Body)
	}
	elsewhere := `{"paper_key":"doi:10.9999/not-in-this-project","runtime":"local","session_id":"x","phase":"done"}`
	if response := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", elsewhere, "me"); response.Code != http.StatusNotFound {
		t.Fatalf("a paper the project does not hold = %d: %s", response.Code, response.Body)
	}
	keyless := `{"runtime":"local","session_id":"x","phase":"done"}`
	if response := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", keyless, "me"); response.Code != http.StatusBadRequest {
		t.Fatalf("a record naming no paper = %d: %s", response.Code, response.Body)
	}
}

// The route is gated like every other operator route: it is registered through
// the gated helper, so the enumerable-routes test covers it too.
func TestTheImportRouteIsGated(t *testing.T) {
	d := testDeps(GateFunc(func(*http.Request) (bool, string) { return false, "not_granted" }))
	h := NewRouter(d).Handler()
	response := do(t, h, http.MethodPost, "/projects/p-1/sessions/import", importedBody, "stranger")
	if response.Code != http.StatusForbidden {
		t.Fatalf("an ungranted caller = %d: %s", response.Code, response.Body)
	}
	registered := false
	for _, route := range NewRouter(d).GatedRoutes() {
		if route.Pattern == "/projects/{id}/sessions/import" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("the import route is not in the gated set")
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}

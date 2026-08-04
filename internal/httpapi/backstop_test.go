package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/project"
)

// The overdraft floor is not published, and this is the test that keeps it that
// way (011).
//
// A number a project's own members can read is an allowance they plan around,
// which is precisely what a backstop must not become. Behavioural inference
// from a run that dies at a repeatable balance is not worth engineering
// against; the code paths that would hand somebody the number outright are, and
// they are what this walks: every payload a member reads, and the client's
// whole string catalogue.
func TestTheOverdraftFloorIsPublishedNowhere(t *testing.T) {
	d := testDeps(allowAll)
	ledger := credit.NewMemStore(nil, d.Members.(credit.Members))
	d.Jobs = job.NewMemStore(job.WithCredits(ledger, fixedReserve(reserve)))
	d.Credits = ledger
	d.Reserve = fixedReserve(reserve)
	rt := NewRouter(d)
	ctx := context.Background()

	me := Identity{Sub: "u", Email: "u@example.com"}
	topUp(t, ledger, me, 10*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", `{"url":"https://arxiv.org/abs/2301.03711"}`, me.Sub, me.Email))
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit = %d %s", w.Code, w.Body.String())
	}
	var started struct {
		JobID     string `json:"job_id"`
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &started)

	// Give the project a floor of its own, so a leak would have something
	// distinctive to leak.
	const secret = int64(4242424242)
	if err := d.Projects.(project.Store).SetCreditCap(ctx, started.ProjectID, secret); err != nil {
		t.Fatalf("set cap: %v", err)
	}

	// Every payload this member can read about themselves and their work.
	for _, path := range []string{
		"/projects", "/projects/" + started.ProjectID, "/jobs", "/jobs/" + started.JobID, "/shared", "/public",
	} {
		w := httptest.NewRecorder()
		rt.Handler().ServeHTTP(w, asPrincipal("GET", path, "", me.Sub, me.Email))
		body := w.Body.String()
		if strings.Contains(body, strconv.FormatInt(secret, 10)) {
			t.Errorf("GET %s carries the project's overdraft floor: %s", path, body)
		}
		for _, word := range []string{"overdraft", "credit_cap", "cap"} {
			if strings.Contains(strings.ToLower(body), word) {
				t.Errorf("GET %s mentions %q: %s", path, word, body)
			}
		}
	}

	// And the platform default is not on them either — it would be the number
	// every project without an override runs against.
	def := strconv.FormatInt(platform.DefaultOverdraftCap, 10)
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("GET", "/projects/"+started.ProjectID, "", me.Sub, me.Email))
	if strings.Contains(w.Body.String(), def) {
		t.Errorf("the project payload carries the platform floor: %s", w.Body.String())
	}
}

// The client never learns the number either — not as a string it could render,
// and not as a field it could read. A catalogue entry explaining the overdraft
// would publish it just as surely as an API field.
func TestTheClientCatalogueNeverNamesTheFloor(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"frontend/src/i18n.ts", "frontend/src/api.ts"} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := strings.ToLower(string(raw))
		for _, word := range []string{"overdraft", "credit_cap", "creditcap"} {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %q — the floor must reach no client surface", rel, word)
			}
		}
	}
}

// A killed run tells its owner that it failed and not why. That is withholding
// a reason rather than asserting a false one — and it is deliberate: the reason
// is the number, and the number is what must not be readable.
func TestAKilledRunRecordsNothingAboutMoney(t *testing.T) {
	cause := job.GenericFailure
	for _, word := range []string{"credit", "budget", "overdraft", "balance", "cap", "money", "spend"} {
		if strings.Contains(strings.ToLower(cause), word) {
			t.Errorf("the recorded cause %q names %q", cause, word)
		}
	}
	if cause == "" {
		t.Error("a killed run must record something; an empty cause reads as a bug")
	}
}

// repoRoot walks up to the module root, so the catalogue test does not depend
// on where `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root")
	return ""
}

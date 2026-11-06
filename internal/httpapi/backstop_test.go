// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
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

// TestTheOverdraftFloorIsPublishedNowhere checks that the overdraft floor is
// not published (011).
//
// A number a project's own members can read becomes an allowance they plan
// around, which a backstop must not become. Inferring the number from a run
// that dies at a repeatable balance is not worth engineering against. The code
// paths that would return the number directly are, and this test covers them:
// every payload a member reads, and the client's whole string catalog.
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

	projectID := createHolding(t, rt, me, "https://arxiv.org/abs/2301.03711")
	allocate(t, rt, me, projectID, 4*credit.Dollar)
	w := start(rt, me, projectID, "arxiv:2301.03711")
	if w.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	var started struct {
		JobID     string `json:"job_id"`
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &started)
	started.ProjectID = projectID

	// Give the project a floor of its own, so a leak would carry a distinctive
	// value.
	const secret = int64(4242424242)
	if err := d.Projects.(project.Store).SetCreditCap(ctx, started.ProjectID, secret); err != nil {
		t.Fatalf("set cap: %v", err)
	}

	// Every payload this member can read about themselves and their work.
	for _, path := range []string{
		"/projects", "/projects/" + started.ProjectID, "/jobs", "/jobs/" + started.JobID, "/public",
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

	// The platform default is not in them either. It is the number every
	// project without an override runs against.
	def := strconv.FormatInt(platform.DefaultOverdraftCap, 10)
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("GET", "/projects/"+started.ProjectID, "", me.Sub, me.Email))
	if strings.Contains(w.Body.String(), def) {
		t.Errorf("the project payload carries the platform floor: %s", w.Body.String())
	}
}

// TestTheMemberFacingClientTypesNeverNameTheFloor checks that the client
// learns the floor on exactly one screen, the admin console, and that the
// member-facing types do not mention it.
//
// An earlier version of this test banned the word anywhere in the client, and
// was wrong. An admin sets this number, so the console names it in a label, a
// hint and a request body. The invariant is not that the string never appears
// but that nothing a project member reads carries it. The check is therefore
// on the member-facing types, the two the app renders on a project screen and
// in the header, rather than on the whole file.
func TestTheMemberFacingClientTypesNeverNameTheFloor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "frontend/src/api.ts"))
	if err != nil {
		t.Fatalf("read api.ts: %v", err)
	}
	text := string(raw)
	for _, typ := range []string{"ProjectCredits", "Credits"} {
		decl := declarationOf(t, text, typ)
		for _, word := range []string{"overdraft", "cap", "floor"} {
			if strings.Contains(strings.ToLower(decl), word) {
				t.Errorf("the member-facing type %s mentions %q: %s", typ, word, decl)
			}
		}
	}
	// The number itself appears nowhere in the client. A default compiled into
	// the UI would publish it whatever the types say.
	for _, rel := range []string{"frontend/src/api.ts", "frontend/src/App.tsx", "frontend/src/i18n.ts"} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.Contains(string(body), strconv.FormatInt(platform.DefaultOverdraftCap, 10)) {
			t.Errorf("%s hard-codes the platform floor", rel)
		}
	}
}

// declarationOf returns the source of one exported type, from its name to the
// blank line after it, which is enough to read its fields without parsing
// TypeScript.
func declarationOf(t *testing.T, src, name string) string {
	t.Helper()
	at := strings.Index(src, "export type "+name+" = ")
	if at < 0 {
		t.Fatalf("no declaration of %s in api.ts", name)
	}
	end := strings.Index(src[at:], "\n\n")
	if end < 0 {
		end = len(src) - at
	}
	return src[at : at+end]
}

// TestAKilledRunRecordsNothingAboutMoney checks that a killed run tells its
// owner that it failed and not why. The reason is withheld rather than
// replaced with a false one, and deliberately so: the reason is the number,
// and the number must not be readable.
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

// repoRoot walks up to the module root, so the catalog test does not depend
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

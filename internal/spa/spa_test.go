package spa

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexFallback(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	// A deep link that is not a real asset falls back to index.html.
	for _, path := range []string{"/", "/jobs/abc", "/dashboard"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "replichai") {
			t.Errorf("%s: body did not contain the app shell", path)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("%s: content-type %q, want html", path, ct)
		}
	}
}

// TestEmbedSurvivesCleanCheckout guards the committed placeholder. The real
// build output under dist/ is gitignored, so on a clean checkout the only
// thing `//go:embed all:dist` can match is the placeholder. Without it the
// pattern matches nothing and the package does not compile at all, which
// takes down vet, build, and test together and reads as an unrelated
// breakage. This fails first, and says why.
func TestEmbedSurvivesCleanCheckout(t *testing.T) {
	entries, err := fs.ReadDir(embedded, "dist")
	if err != nil {
		t.Fatalf("read embedded dist: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "PLACEHOLDER" {
			return
		}
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	t.Fatalf("internal/spa/dist/PLACEHOLDER is not committed; dist holds only build output %v, "+
		"so a clean checkout cannot satisfy //go:embed all:dist", names)
}

// TestHandlerFallsBackWhenNotBuilt covers the no-build path: a binary
// compiled from a clean checkout has no index.html, and the handler serves
// the build instructions rather than failing to construct.
func TestHandlerFallsBackWhenNotBuilt(t *testing.T) {
	if _, err := fs.ReadFile(embedded, "dist/index.html"); err == nil {
		t.Skip("a real frontend build is present in this tree")
	}
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not built into this binary") {
		t.Errorf("body did not explain the missing build: %s", w.Body.String())
	}
}

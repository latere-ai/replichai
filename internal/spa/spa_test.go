package spa

import (
	"compress/gzip"
	"io"
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

// The bundle went out uncompressed: nothing in front of this server compresses,
// and net/http does not do it on its own, so every visit shipped ~350 KB of
// JavaScript raw. On a slow link that is the difference between a page that
// loads and one somebody gives up on.
func TestAssetsAreCompressedForClientsThatAcceptIt(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	// A cache that stores the gzipped body under a key it shares with a client
	// that cannot read gzip would serve that client bytes it cannot decode.
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to include Accept-Encoding", got)
	}
	// The body really is gzip, and really does decode back to the page.
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !strings.Contains(string(body), "<") {
		t.Errorf("decompressed body does not look like html: %q", body)
	}
	// A Content-Length describing the uncompressed size would be a lie.
	if w.Header().Get("Content-Length") != "" {
		t.Error("Content-Length survived compression; it describes the wrong bytes")
	}
}

// A client that cannot decompress must still get a readable page.
func TestClientWithoutGzipGetsPlainBytes(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q for a client that did not ask for it", got)
	}
	if !strings.Contains(w.Body.String(), "<") {
		t.Errorf("body = %q, want html", w.Body.String())
	}
}

// index.html names the hashed assets, so caching it would pin a reader to an
// old build; the hashed assets themselves never change and are cached hard.
func TestCachingDistinguishesTheShellFromItsAssets(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("shell Cache-Control = %q, want no-cache", got)
	}
}

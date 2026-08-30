// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	// A deep link that is not a real asset falls back to index.html, so the
	// client-side router can resolve it. Which addresses those are is 042c's
	// list: /jobs/abc and /dashboard were app paths when this test was written
	// and are not addresses this app has, so they now carry the shell with a
	// 404 rather than answering 200 with the dashboard.
	for _, path := range []string{"/", "/registry", "/p/abc", "/p/abc/s/xyz", "/u/ada"} {
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

// AC1: an address this app does not have says so, and still carries the app —
// the status is the truth for a crawler, the body is a page a person who
// mistyped can navigate from.
func TestAnAddressThatDoesNotExistIs404(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	for _, path := range []string{"/dashboard", "/jobs/abc", "/this-page-does-not-exist-xyz", "/p"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
		if got := w.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s: X-Robots-Tag %q, want noindex", path, got)
		}
		if !strings.Contains(w.Body.String(), "replichai") {
			t.Errorf("%s: a 404 did not carry the app", path)
		}
	}
}

// AC5: the screens a signed-out reader renders nothing at are marked, whatever
// robots.txt says — that file governs fetching, this governs indexing.
func TestTheOperatorScreensAreNotIndexed(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	for _, path := range []string{"/wallet", "/settings", "/settings/profile", "/console", "/new", "/trash"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, w.Code)
		}
		if got := w.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s: X-Robots-Tag %q, want noindex", path, got)
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

// Some assets arrive compressed, and gzipping them again is pure loss: CPU on
// every uncached load, a payload that comes out slightly larger, and no
// Content-Length, so the browser cannot show progress. 040 added two woff2
// faces to the served set and made that visible; the PNGs had been paying it
// since the card images landed.
func TestHandlerDoesNotRecompressPackedAssets(t *testing.T) {
	if _, err := fs.ReadFile(embedded, "dist/index.html"); err != nil {
		t.Skip("no frontend build in this tree")
	}
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	// Ask for every asset the way a browser does, and check what came back.
	for _, tc := range []struct {
		path string
		gzip bool
	}{
		{"/fonts/instrument-serif-italic.woff2", false},
		{"/og.png", false},
		{"/favicon.svg", true},
		{"/index.html", true},
	} {
		if !exists(mustSub(t), strings.TrimPrefix(tc.path, "/")) {
			continue // this tree's build does not carry it
		}
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		got := w.Header().Get("Content-Encoding") == "gzip"
		if got != tc.gzip {
			t.Errorf("%s: Content-Encoding gzip = %v, want %v", tc.path, got, tc.gzip)
		}
		if !got && w.Header().Get("Content-Length") == "" {
			t.Errorf("%s: served uncompressed without a Content-Length", tc.path)
		}
	}
}

// The faces the shared footer's logotype is set in have to reach the browser
// from this binary: the stylesheet names Instrument Serif, and without these
// the Latere logotype renders in whatever serif the reader happens to have.
func TestHandlerServesTheFonts(t *testing.T) {
	if _, err := fs.ReadFile(embedded, "dist/index.html"); err != nil {
		t.Skip("no frontend build in this tree")
	}
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	for _, name := range []string{"italic", "regular"} {
		path := "/fonts/instrument-serif-" + name + ".woff2"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, w.Code)
		}
		// The SPA fallback answers 200 with HTML for anything it cannot find,
		// so the status alone proves nothing.
		if ct := w.Header().Get("Content-Type"); ct != "font/woff2" {
			t.Errorf("%s: content-type %q, want font/woff2", path, ct)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
			t.Errorf("%s: cache-control %q carries no lifetime", path, cc)
		}
	}
}

func mustSub(t *testing.T) fs.FS {
	t.Helper()
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	return dist
}

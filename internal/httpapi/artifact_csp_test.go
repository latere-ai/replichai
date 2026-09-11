// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestArtifactSVGCannotScript checks the header that keeps a committed file
// from running as script on this origin.
//
// GET /jobs/{id}/artifact is an open route serving bytes an agent committed to a
// repository, and a .svg is served as image/svg+xml so the app can render it in
// an <img>. An SVG opened directly in a tab is a document rather than an image:
// it runs any <script> it carries, on this origin, against the reader's
// session. nosniff does not help, because the type is declared rather than
// sniffed, so the response carries a Content-Security-Policy that sandboxes
// it into an opaque origin with scripts disabled.
func TestArtifactSVGCannotScript(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path=plot.svg", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("artifact = %d, want 200", w.Code)
	}
	// The type stays image/svg+xml. The app renders a committed SVG through an
	// <img> element, since frontend/src/artifact.ts treats svg as an image, and
	// text/plain would stop it rendering without any error.
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy: a committed .svg opened in a tab would run its own script on this origin")
	}
	// A sandbox directive with no allow-scripts token is what disables script.
	// An allow-scripts token anywhere would enable it again.
	if !cspHasToken(csp, "sandbox") {
		t.Errorf("CSP = %q, want a sandbox directive", csp)
	}
	if cspHasToken(csp, "allow-scripts") {
		t.Errorf("CSP = %q, must not allow scripts", csp)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff was dropped")
	}
}

// TestArtifactTextIsSandboxedToo checks that the header is unconditional
// rather than an svg special case, so a type added to artifactContentType
// later is covered without a further change.
func TestArtifactTextIsSandboxedToo(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path=README.md", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("artifact = %d, want 200", w.Code)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !cspHasToken(csp, "sandbox") {
		t.Errorf("CSP = %q, want a sandbox directive on every artifact response", csp)
	}
}

// cspHasToken reports whether a CSP header carries a bare token, matching whole
// tokens so "sandbox" cannot be found inside a longer word.
func cspHasToken(csp, token string) bool {
	for directive := range strings.SplitSeq(csp, ";") {
		if slices.Contains(strings.Fields(directive), token) {
			return true
		}
	}
	return false
}

// TestArtifactPathCannotInjectForgeQuery checks the other half of the path
// rule.
//
// The artifact path is concatenated directly into the forge URL, because
// github.Client.GetFileRaw builds ".../contents/" + path. A `?` in the path is
// therefore not a character in a filename: it starts a query string on the
// contents API, where `ref` chooses which branch, tag or commit to read. An
// anonymous caller could otherwise ask for a tree the project never published
// but the platform credential can still see. A `#` truncates the URL to the
// repository root for the same reason. Both are refused at the edge, alongside
// `..` and the other refused characters.
//
// The query decoder unescapes once, so `%3F` arrives here as a bare `?` and
// passes the existing percent check, which is why this needs its own rule.
func TestArtifactPathCannotInjectForgeQuery(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	for _, path := range []string{
		"README.md%3Fref=secret-branch", // ?ref= selects a different tree of the same repository.
		"README.md%23fragment",          // # truncates the forge URL.
	} {
		w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path="+path, "", "me")
		if w.Code != http.StatusBadRequest {
			t.Errorf("path %q = %d, want 400 bad_path", path, w.Code)
		}
	}

	// The plain name still works: the rule refuses URL metacharacters, not
	// ordinary filenames.
	if w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path=src/main.py", "", "me"); w.Code != http.StatusOK {
		t.Errorf("ordinary path = %d, want 200", w.Code)
	}
}

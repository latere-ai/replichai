package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestArtifactSVGCannotScript pins the guard that keeps a committed file from
// running as script on this origin.
//
// GET /jobs/{id}/artifact is an open route serving bytes an agent committed to a
// repository, and a .svg is served as image/svg+xml so the app can render it in
// an <img>. An SVG opened directly in a tab is a document, not a picture: it
// runs any <script> it carries, on our origin, against the reader's session.
// nosniff does not help — the type is declared, not sniffed — so the response
// carries a Content-Security-Policy that sandboxes it into an opaque origin with
// scripts off.
func TestArtifactSVGCannotScript(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path=plot.svg", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("artifact = %d, want 200", w.Code)
	}
	// The type stays image/svg+xml: the app renders a committed SVG through
	// <img src=…> (frontend/src/artifact.ts treats svg as an image), and text/plain
	// would silently stop it rendering at all.
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy: a committed .svg opened in a tab would run its own script on this origin")
	}
	// sandbox with no allow-scripts is what disables script; an allow-scripts
	// token anywhere would hand it straight back.
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

// TestArtifactTextIsSandboxedToo pins that the header is unconditional rather
// than an svg special case: a type added to artifactContentType later inherits
// the guard instead of having to remember it.
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
	for _, directive := range strings.Split(csp, ";") {
		if slices.Contains(strings.Fields(directive), token) {
			return true
		}
	}
	return false
}

// TestArtifactPathCannotInjectForgeQuery pins the other half of the path rule.
//
// The artifact path is pasted straight into the forge URL
// (github.Client.GetFileRaw concatenates ".../contents/" + path), so a `?` in
// the path is not a character in a filename — it starts a query string on the
// contents API, where `ref` chooses which branch, tag or commit to read. An
// anonymous caller could therefore ask for a tree the project never published
// but the platform credential can still see. A `#` truncates the URL to the
// repository root for the same reason. Both are refused at the edge, alongside
// `..` and the rest.
//
// The query decoder unescapes once, so `%3F` arrives here as a bare `?` and
// slips past the existing percent check — which is why this needs its own rule.
func TestArtifactPathCannotInjectForgeQuery(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	for _, path := range []string{
		"README.md%3Fref=secret-branch", // ?ref= — a different tree of the same repo
		"README.md%23fragment",          // # — truncates the forge URL
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

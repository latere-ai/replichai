package seo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const shell = `<head><link rel="icon" href="/f.svg" />` +
	`<!--seo--><title>default</title><meta name="description" content="d" /><!--/seo-->` +
	`<script>theme()</script></head>`

func TestDescribeReplacesTheRegionAndNothingElse(t *testing.T) {
	got := string(Describe([]byte(shell), Page{Title: "A paper", Canonical: "https://x.test/p/1"}, true))

	if n := strings.Count(got, "<title>"); n != 1 {
		t.Errorf("%d titles, want 1", n)
	}
	if !strings.Contains(got, "<title>A paper</title>") || strings.Contains(got, "<title>default</title>") {
		t.Error("the default title survived")
	}
	for _, keep := range []string{`<link rel="icon" href="/f.svg" />`, `<script>theme()</script>`, "<!--seo-->", "<!--/seo-->"} {
		if !strings.Contains(got, keep) {
			t.Errorf("substitution disturbed %q", keep)
		}
	}
}

// A page with no title is one nobody may read. There is nothing truer to say
// about it than the shell already says, and the response is marked instead.
func TestAPageWithNoTitleKeepsTheDefaultHead(t *testing.T) {
	if got := string(Describe([]byte(shell), Page{NoIndex: true}, true)); got != shell {
		t.Error("a not-indexed page rewrote the head")
	}
	if got := string(Describe([]byte(shell), Page{Title: "x"}, false)); got != shell {
		t.Error("an undescribed request rewrote the head")
	}
}

// A shell with no markers is a build this repository did not produce — the
// committed placeholder, or a stale dist. It goes out unchanged rather than
// gaining a second title.
func TestAShellWithoutMarkersIsServedUnchanged(t *testing.T) {
	plain := "<head><title>only</title></head>"
	if got := string(Describe([]byte(plain), Page{Title: "new"}, true)); got != plain {
		t.Errorf("a marker-less shell was rewritten: %q", got)
	}
}

// Titles are researcher-supplied text: quotes, angle brackets and a `</script>`
// are ordinary, not hostile, and must not become markup.
func TestEveryValueIsEscaped(t *testing.T) {
	head := Page{
		Title:       `</script><img src=x onerror="boom()">`,
		Description: `a "quoted" & pointed <thing>`,
		Canonical:   "https://x.test/p/1",
		JSONLD:      map[string]any{"name": `</script><script>boom()</script>`},
	}.Head()

	if strings.Contains(head, "<img") || strings.Contains(head, "<script>boom") {
		t.Errorf("markup survived escaping: %s", head)
	}
	if strings.Count(head, "<script") != 1 {
		t.Error("the ld+json element was closed early")
	}
	if !strings.Contains(head, "&amp;") || !strings.Contains(head, "&#34;") {
		t.Error("an attribute value was not escaped")
	}
}

// og:url is the canonical, always: two addresses claiming to be the page is the
// ambiguity a canonical exists to remove.
func TestTheCardPointsAtTheCanonical(t *testing.T) {
	head := Page{Title: "t", Canonical: "https://x.test/p/1", Image: "https://x.test/og.png"}.Head()
	if !strings.Contains(head, `<meta property="og:url" content="https://x.test/p/1" />`) {
		t.Error("og:url is not the canonical")
	}
	if !strings.Contains(head, `content="https://x.test/og.png"`) {
		t.Error("the card image is not absolute")
	}
	// A page built without one still renders a valid card.
	if !strings.Contains(Page{Title: "t"}.Head(), DefaultBase+CardImage) {
		t.Error("a page with no image did not fall back to the site's own")
	}
}

func TestABaseURLIsAlwaysAbsolute(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://replichai.test", "https://replichai.test/p/1"},
		{"https://replichai.test/", "https://replichai.test/p/1"},
		{" ", DefaultBase + "/p/1"},
		{"", DefaultBase + "/p/1"},
	} {
		if got := NewSite(tc.base).Abs(ProjectPath("1")); got != tc.want {
			t.Errorf("NewSite(%q).Abs = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestWriteShellMarksWhatMustNotBeIndexed(t *testing.T) {
	serve := func(p Page, described bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/p/1", nil)
		if described {
			r = r.WithContext(With(r.Context(), p))
		}
		w := httptest.NewRecorder()
		WriteShell(w, r, []byte(shell))
		return w
	}
	if got := serve(Page{NoIndex: true}, true).Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag %q, want noindex", got)
	}
	if got := serve(Page{Title: "public"}, true).Header().Get("X-Robots-Tag"); got != "" {
		t.Errorf("a public page was marked %q", got)
	}
	if got := serve(Page{}, false).Header().Get("X-Robots-Tag"); got != "" {
		t.Errorf("an undescribed request was marked %q", got)
	}
}

// The paths mirror the client's pathOf (013); routes.ts is the other copy.
func TestThePathsAreTheClientsPaths(t *testing.T) {
	if ProjectPath("abc") != "/p/abc" || SessionPath("abc", "s1") != "/p/abc/s/s1" || ProfilePath("ada") != "/u/ada" {
		t.Error("a path stopped agreeing with the client's")
	}
}

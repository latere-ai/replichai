package seo

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The two copies, held together (042b, 042c).
//
// Go holds a second copy of the client's screen table and a static robots.txt
// sits in the frontend's public directory. Neither can be derived from the other
// at run time — one has to answer before any script runs, the other is a file a
// crawler asks for before it asks for anything else — so what keeps them honest
// is this test, which reads the client's own source rather than a description of
// it.

const routesTS = "../../frontend/src/routes.ts"

// screenPaths extracts the keys of SCREENS in routes.ts: the fixed addresses the
// client resolves without a pattern.
func screenPaths(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(routesTS)
	if err != nil {
		t.Fatalf("reading the client's routes: %v", err)
	}
	block := regexp.MustCompile(`(?s)const SCREENS[^{]*\{(.*?)\n\}`).FindSubmatch(src)
	if block == nil {
		t.Fatal("SCREENS is no longer a literal in routes.ts; this guard needs rewriting, not deleting")
	}
	var out []string
	for _, m := range regexp.MustCompile(`'(/[^']*)'\s*:`).FindAllSubmatch(block[1], -1) {
		out = append(out, string(m[1]))
	}
	if len(out) < 5 {
		t.Fatalf("found only %d screens in routes.ts; the extraction is broken", len(out))
	}
	return out
}

// AC4: every address the client has, the server has.
func TestTheServerKnowsEveryScreenTheClientHas(t *testing.T) {
	for _, path := range screenPaths(t) {
		if !Known(path) {
			t.Errorf("routes.ts serves %s and the server answers 404 for it", path)
		}
		if !slices.Contains(Screens, path) {
			t.Errorf("%s is a client screen and is missing from Screens", path)
		}
	}
	// And nothing the server claims is a screen has been deleted from the
	// client, which is the direction that leaves an address answering 200 with
	// a screen that no longer exists.
	client := screenPaths(t)
	for _, path := range Screens {
		// /settings/profile is a tab of /settings, matched by routes.ts with a
		// pattern rather than named in SCREENS (024a), so it is legitimately
		// absent from the literal.
		if path == "/settings/profile" {
			continue
		}
		if !slices.Contains(client, path) {
			t.Errorf("the server serves %s and routes.ts no longer has it", path)
		}
	}
}

// The record shapes, which routes.ts matches with regexps rather than naming.
func TestTheRecordShapesAreAddresses(t *testing.T) {
	for _, path := range []string{
		"/p/abc", "/p/abc/verdict", "/p/abc/s/xyz", "/p/abc/s/xyz/artifacts", "/u/ada", "/u/ADA",
		"/wallet/", // a trailing slash is the same address, as it is on the client
		// A session link cut short still identifies its project, which is what
		// routes.ts resolves it to rather than sending the reader back to the
		// dashboard. The server agrees rather than 404ing a link somebody
		// truncated by hand.
		"/p/abc/s",
	} {
		if !Known(path) {
			t.Errorf("%s is an address this app has and the server 404s it", path)
		}
	}
	for _, path := range []string{
		"/dashboard", "/jobs/abc", "/p", "/u", "/u/ada/extra", "/p/abc/x/y/z", "/../etc",
	} {
		if Known(path) {
			t.Errorf("%s is not an address this app has and the server serves it", path)
		}
	}
}

// The operator screens are the ones a signed-out reader — which is what a
// crawler is — renders nothing at.
func TestTheOperatorScreensAreNamedInBothPlaces(t *testing.T) {
	robots, err := os.ReadFile("../../frontend/public/robots.txt")
	if err != nil {
		t.Fatalf("reading robots.txt: %v", err)
	}
	text := string(robots)
	for _, path := range Screens {
		if !Operator(path) {
			continue
		}
		// A tab is not fetched separately from the screen it is on, so
		// Disallow: /settings covers /settings/profile.
		if strings.Count(path, "/") > 1 {
			continue
		}
		if !strings.Contains(text, "Disallow: "+path+"\n") {
			t.Errorf("%s is an operator screen and robots.txt does not disallow it", path)
		}
	}
	// AC1: the sitemap is named by absolute URL — a crawler reading robots.txt
	// resolves it against nothing — and the public zone is not disallowed.
	if !strings.Contains(text, "Sitemap: "+DefaultBase+"/sitemap.xml") {
		t.Error("robots.txt does not name the sitemap by absolute URL")
	}
	if !strings.Contains(text, "Allow: /\n") {
		t.Error("robots.txt does not allow the public zone")
	}
	for _, open := range []string{"/registry", "/p/", "/u/"} {
		if strings.Contains(text, "Disallow: "+open) {
			t.Errorf("robots.txt disallows %s, which is the public zone", open)
		}
	}
}

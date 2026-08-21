package httpapi

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
)

// The sitemap (042b). Every assertion is on the file a crawler parses, decoded
// as XML rather than grepped, because "it contains this string" would pass on a
// document no crawler can read.

func decodeSitemap(t *testing.T, body string) urlset {
	t.Helper()
	var set urlset
	if err := xml.Unmarshal([]byte(body), &set); err != nil {
		t.Fatalf("the sitemap does not parse as XML: %v\n%s", err, body)
	}
	if set.XMLName.Local != "urlset" {
		t.Errorf("root element is %q, want urlset", set.XMLName.Local)
	}
	return set
}

func locations(set urlset) map[string]string {
	out := map[string]string{}
	for _, u := range set.URLs {
		out[u.Loc] = u.LastMod
	}
	return out
}

// AC2, AC3: the file lists the public zone — the landing, the registry, each
// published project, each published session under one, and each signer's page.
func TestTheSitemapListsThePublicZone(t *testing.T) {
	d := seoDeps()
	ctx := context.Background()
	if _, err := d.Profiles.Save(ctx, profile.Profile{
		Principal: "owner-sub", DisplayName: "Ada Lovelace", Handle: "ada", Public: true,
	}); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	p := describedProject(t, d)
	published := session(t, d, p.ID, "doi:10.1007/3-540-48157-5_9", true)
	pending := session(t, d, p.ID, "arxiv:2401.00001", false)
	h := NewRouter(d).Handler()

	w := do(t, h, "GET", "/sitemap.xml", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/xml") {
		t.Errorf("content-type %q, want application/xml", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != sitemapTTL {
		t.Errorf("cache-control %q, want %q", cc, sitemapTTL)
	}
	at := locations(decodeSitemap(t, w.Body.String()))

	for _, want := range []string{
		"https://replichai.test/",
		"https://replichai.test/registry",
		"https://replichai.test/p/" + p.ID,
		"https://replichai.test/p/" + p.ID + "/s/" + published.ID,
		"https://replichai.test/u/ada",
	} {
		if _, ok := at[want]; !ok {
			t.Errorf("the sitemap does not list %s", want)
		}
	}
	// A session with nothing published is an address whose own head says
	// noindex; listing it would be the sitemap contradicting the page.
	if _, ok := at["https://replichai.test/p/"+p.ID+"/s/"+pending.ID]; ok {
		t.Error("the sitemap lists a session that has published nothing")
	}
	// AC6: a project's lastmod is a date the app can substantiate.
	if mod := at["https://replichai.test/p/"+p.ID]; !strings.HasPrefix(mod, "20") || !strings.HasSuffix(mod, "Z") {
		t.Errorf("project lastmod is %q, want a W3C UTC timestamp", mod)
	}
}

// AC4: nothing private, trashed, unpublished or withdrawn is listed.
func TestTheSitemapListsNothingPrivate(t *testing.T) {
	d := seoDeps()
	ctx := context.Background()
	private, err := d.Projects.Create(ctx, "owner-sub", "A private working title")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	withdrawn := describedProject(t, d)
	if err := d.Projects.SetPrivate(ctx, withdrawn.ID, project.Withdrawal{By: "owner-sub", Reason: "an error"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	trashed := describedProject(t, d)
	if err := d.Projects.Trash(ctx, trashed.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	// A signer whose page is not public: the sign-off stands, the link does not.
	if _, err := d.Profiles.Save(ctx, profile.Profile{
		Principal: "owner-sub", DisplayName: "Ada Lovelace", Handle: "ada", Public: false,
	}); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	open := describedProject(t, d)

	body := do(t, NewRouter(d).Handler(), "GET", "/sitemap.xml", "", "").Body.String()
	at := locations(decodeSitemap(t, body))

	for name, id := range map[string]string{"private": private.ID, "withdrawn": withdrawn.ID, "trashed": trashed.ID} {
		if _, ok := at["https://replichai.test/p/"+id]; ok {
			t.Errorf("the sitemap lists a %s project", name)
		}
	}
	if _, ok := at["https://replichai.test/u/ada"]; ok {
		t.Error("the sitemap lists a profile its owner has not made public")
	}
	if _, ok := at["https://replichai.test/p/"+open.ID]; !ok {
		t.Error("the sitemap dropped the one project that is public")
	}
}

// AC5: a listed URL and the canonical of the page it names are one string.
func TestAListedURLIsThatPagesCanonical(t *testing.T) {
	d := seoDeps()
	describedProject(t, d)
	h := NewRouter(d).Handler()

	for loc := range locations(decodeSitemap(t, do(t, h, "GET", "/sitemap.xml", "", "").Body.String())) {
		path := strings.TrimPrefix(loc, "https://replichai.test")
		page := do(t, h, "GET", path, "", "")
		if page.Code != http.StatusOK {
			t.Errorf("%s: status %d — the sitemap lists an address that does not answer", loc, page.Code)
			continue
		}
		if got := page.Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("%s: the sitemap lists a page marked %q", loc, got)
		}
		if !strings.Contains(page.Body.String(), `<link rel="canonical" href="`+loc+`" />`) {
			t.Errorf("%s: the page's canonical is not the address the sitemap lists", loc)
		}
	}
}

// AC7: a store that cannot answer produces an error, never an empty file. An
// empty urlset says there is nothing to index, and a crawler believes it.
func TestASitemapIsNeverEmptyByAccident(t *testing.T) {
	d := seoDeps()
	d.Projects = failingProjects{Store: d.Projects.(project.Store), publicErr: context.DeadlineExceeded}
	w := do(t, NewRouter(d).Handler(), "GET", "/sitemap.xml", "", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "<urlset") {
		t.Error("a failed read produced a sitemap")
	}
}

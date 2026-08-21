package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/seo"
)

// The head the server writes (042a).
//
// Every assertion below is on the bytes a crawler or a link unfurler receives,
// because that reader never runs the script that would fix a head client-side.
// The shell is the real one's shape — the markers, and something outside them
// that substitution must not disturb — served through the same seo.WriteShell
// the built frontend is served through, so these tests and production cannot be
// two implementations of one rule.
const testShell = `<!doctype html><html lang="en"><head><meta charset="UTF-8" />` +
	`<link rel="icon" href="/favicon.svg" />` +
	`<!--seo-->` +
	`<title>ReplicHAI — does the paper actually reproduce?</title>` +
	`<meta name="description" content="the default" />` +
	`<!--/seo-->` +
	`<script>var theme = 1</script>` +
	`</head><body><div id="root"></div></body></html>`

func shellHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		seo.WriteShell(w, r, []byte(testShell))
	})
}

// seoDeps is testDeps with a frontend attached, which is what turns the public
// app addresses on.
func seoDeps() Deps {
	d := testDeps(allowAll)
	d.SPA = shellHandler()
	d.BaseURL = "https://replichai.test"
	return d
}

// describedProject seeds one published project holding one paper with a real
// bibliography, and returns it. The paper is a DOI so the structured data has an
// identifier to resolve.
func describedProject(t *testing.T, d Deps) project.Project {
	t.Helper()
	ctx := context.Background()
	p, err := d.Projects.Create(ctx, "owner-sub", "There is more to context than location")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Projects.AddPaper(ctx, p.ID, project.Paper{
		Key:   "doi:10.1007/3-540-48157-5_9",
		Ref:   "https://doi.org/10.1007/3-540-48157-5_9",
		Title: "There is more to context than location",
		Record: paper.Record{
			Authors:   []string{"Albrecht Schmidt", "Michael Beigl", "Hans-W Gellersen"},
			Venue:     "Computers & Graphics",
			Year:      1999,
			Publisher: "Elsevier",
		},
	}); err != nil {
		t.Fatalf("add paper: %v", err)
	}
	if err := d.Projects.SetDescription(ctx, p.ID, "The 1999 paper argues that context-aware computing extends beyond spatial information."); err != nil {
		t.Fatalf("describe: %v", err)
	}
	if err := d.Projects.SetPublic(ctx, p.ID, project.SignOff{
		By: "owner-sub", AttestationVersion: "v2", Name: "Ada Lovelace",
		Institution: "LMU Munich", ORCID: "0000-0002-1825-0097",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	out, err := d.Projects.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return out
}

// session enqueues one run of a paper under a project, and takes it to
// published when asked — the state a session page has evidence to describe.
func session(t *testing.T, d Deps, projectID, paperKey string, publish bool) job.Job {
	t.Helper()
	ctx := context.Background()
	j, err := d.Jobs.Enqueue(ctx, job.Submission{Principal: "owner-sub", Kind: job.KindAudit, Project: projectID, Ref: "ref", PaperKey: paperKey})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !publish {
		return j
	}
	adv := d.Jobs.(*job.MemStore)
	for _, phase := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
		at := phase
		if err := adv.Advance(ctx, j.ID, at, func(jb *job.Job) {
			if at == job.PhasePublishing {
				jb.RepoName = "open-hai/ctx-1"
			}
		}); err != nil {
			t.Fatalf("advance %s: %v", at, err)
		}
	}
	got, _ := d.Jobs.Get(ctx, j.ID)
	return got
}

// AC1: a public project's address names that project.
func TestAPublicProjectDescribesItself(t *testing.T) {
	d := seoDeps()
	p := describedProject(t, d)
	h := NewRouter(d).Handler()

	body := do(t, h, "GET", "/p/"+p.ID, "", "").Body.String()
	for _, want := range []string{
		"<title>There is more to context than location</title>",
		`<meta property="og:title" content="There is more to context than location" />`,
		"context-aware computing extends beyond spatial information",
		`<link rel="canonical" href="https://replichai.test/p/` + p.ID + `" />`,
		`<meta property="og:url" content="https://replichai.test/p/` + p.ID + `" />`,
		`"@type":"ScholarlyArticle"`,
		`"sameAs":"https://doi.org/10.1007/3-540-48157-5_9"`,
		`"Albrecht Schmidt"`,
		`"datePublished":"1999"`,
		`"name":"Ada Lovelace"`,
		`"https://orcid.org/0000-0002-1825-0097"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the described page is missing %q", want)
		}
	}
	if strings.Contains(body, "does the paper actually reproduce?") {
		t.Error("the default head survived the substitution")
	}
	// The shell's own head is not the head region and must come through intact.
	for _, keep := range []string{`<link rel="icon" href="/favicon.svg" />`, `<script>var theme = 1</script>`, `<div id="root">`} {
		if !strings.Contains(body, keep) {
			t.Errorf("substitution disturbed the shell: %q is gone", keep)
		}
	}
}

// AC2: a private, trashed or unknown project reveals nothing and is not indexed.
// The three cases answer identically on purpose — a head that told them apart
// would report the existence of every private project to anybody walking ids.
func TestAProjectNobodyPublishedIsNotDescribed(t *testing.T) {
	d := seoDeps()
	ctx := context.Background()
	private, err := d.Projects.Create(ctx, "owner-sub", "A private working title")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	trashed := describedProject(t, d)
	if err := d.Projects.Trash(ctx, trashed.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	h := NewRouter(d).Handler()

	for _, tc := range []struct{ name, path, secret string }{
		{"private", "/p/" + private.ID, "A private working title"},
		{"trashed", "/p/" + trashed.ID, "There is more to context than location"},
		{"unknown", "/p/does-not-exist", ""},
	} {
		w := do(t, h, "GET", tc.path, "", "")
		if got := w.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s: X-Robots-Tag %q, want noindex", tc.name, got)
		}
		if !strings.Contains(w.Body.String(), "does the paper actually reproduce?") {
			t.Errorf("%s: the default head was replaced", tc.name)
		}
		if tc.secret != "" && strings.Contains(w.Body.String(), tc.secret) {
			t.Errorf("%s: the title reached crawlable HTML", tc.name)
		}
	}
	// The owner's own request is answered the same way. The injected head keys
	// on whether the record is public, never on who is asking, so there is no
	// identity that turns a private title into indexable bytes.
	w := do(t, h, "GET", "/p/"+private.ID, "", "owner-sub")
	if strings.Contains(w.Body.String(), "A private working title") {
		t.Error("an owner's request injected their private project's title")
	}
}

// AC3: a public profile names its researcher; a private one says nothing.
func TestAProfileIsDescribedOnlyWhenPublic(t *testing.T) {
	d := seoDeps()
	ctx := context.Background()
	if _, err := d.Profiles.Save(ctx, profile.Profile{
		Principal: "sub-1", DisplayName: "Ada Lovelace", Institution: "LMU Munich",
		ORCID: "0000-0002-1825-0097", Handle: "ada", Public: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := d.Profiles.Save(ctx, profile.Profile{
		Principal: "sub-2", DisplayName: "Grace Hopper", Handle: "grace", Public: false,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	h := NewRouter(d).Handler()

	body := do(t, h, "GET", "/u/ada", "", "").Body.String()
	for _, want := range []string{
		"<title>Ada Lovelace · ReplicHAI</title>",
		`"@type":"ProfilePage"`,
		`"affiliation":{"@type":"Organization","name":"LMU Munich"}`,
		`"sameAs":["https://orcid.org/0000-0002-1825-0097"]`,
		`<link rel="canonical" href="https://replichai.test/u/ada" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the profile page is missing %q", want)
		}
	}
	// A handle typed in any case reaches the same page, which is the rule the
	// profile route already follows.
	if !strings.Contains(do(t, h, "GET", "/u/ADA", "", "").Body.String(), "Ada Lovelace") {
		t.Error("an upper-cased handle was not described")
	}
	w := do(t, h, "GET", "/u/grace", "", "")
	if strings.Contains(w.Body.String(), "Grace Hopper") || w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Error("a private profile was described")
	}
}

// AC4: a published session under a public project names the paper it is a run of.
func TestAPublishedSessionNamesItsPaper(t *testing.T) {
	d := seoDeps()
	p := describedProject(t, d)
	ctx := context.Background()
	h := NewRouter(d).Handler()

	// A session that has not begun publishing has no published evidence, so it
	// describes nothing yet.
	// A different paper, because one paper may have only one session in flight.
	unpublished := session(t, d, p.ID, "arxiv:2401.00001", false)
	if w := do(t, h, "GET", "/p/"+p.ID+"/s/"+unpublished.ID, "", ""); w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Error("an unpublished session was described")
	}

	j := session(t, d, p.ID, "doi:10.1007/3-540-48157-5_9", true)
	body := do(t, h, "GET", "/p/"+p.ID+"/s/"+j.ID, "", "").Body.String()
	for _, want := range []string{
		"<title>Reproduction of There is more to context than location · ReplicHAI</title>",
		`<link rel="canonical" href="https://replichai.test/p/` + p.ID + "/s/" + j.ID + `" />`,
		`"@type":"ScholarlyArticle"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the session page is missing %q", want)
		}
	}
	// A session id that belongs to another project is not this address's
	// session, whatever the address says.
	other, err := d.Projects.Create(ctx, "owner-sub", "Another project")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Projects.SetPublic(ctx, other.ID, project.SignOff{By: "owner-sub", AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if w := do(t, h, "GET", "/p/"+other.ID+"/s/"+j.ID, "", ""); w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Error("a session was described under a project it does not belong to")
	}
}

// A session its owner threw away (032) leaves the public surface completely: it
// is neither described nor listed. Both sides ask the job store, and both stores
// hide a deleted row from Get and ListByProject — this pins that, because the
// two predicates agreeing with each other is not the same as either being right.
func TestADeletedSessionLeavesThePublicSurface(t *testing.T) {
	d := seoDeps()
	p := describedProject(t, d)
	j := session(t, d, p.ID, "doi:10.1007/3-540-48157-5_9", true)
	h := NewRouter(d).Handler()

	if w := do(t, h, "GET", "/p/"+p.ID+"/s/"+j.ID, "", ""); w.Header().Get("X-Robots-Tag") != "" {
		t.Fatal("the fixture session is not described, so deleting it would prove nothing")
	}
	if err := d.Jobs.Delete(context.Background(), j.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	w := do(t, h, "GET", "/p/"+p.ID+"/s/"+j.ID, "", "")
	if w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Error("a deleted session is still described")
	}
	if strings.Contains(w.Body.String(), "There is more to context than location") {
		t.Error("a deleted session's paper reached crawlable HTML")
	}
	if body := do(t, h, "GET", "/sitemap.xml", "", "").Body.String(); strings.Contains(body, "/s/"+j.ID) {
		t.Error("the sitemap still lists a deleted session")
	}
}

// AC5: one title, one canonical, and a tab folds to the record it is a view of.
func TestOneTitleAndATabThatFoldsToItsRecord(t *testing.T) {
	d := seoDeps()
	p := describedProject(t, d)
	h := NewRouter(d).Handler()
	want := `<link rel="canonical" href="https://replichai.test/p/` + p.ID + `" />`

	for _, path := range []string{"/p/" + p.ID, "/p/" + p.ID + "/verdict", "/p/" + p.ID + "/people"} {
		body := do(t, h, "GET", path, "", "").Body.String()
		if n := strings.Count(body, "<title>"); n != 1 {
			t.Errorf("%s: %d <title> elements, want 1", path, n)
		}
		if n := strings.Count(body, `rel="canonical"`); n != 1 {
			t.Errorf("%s: %d canonical links, want 1", path, n)
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s: tab did not canonicalise to the project's own address", path)
		}
	}
}

// AC7: a title is researcher-supplied text, and reaches the client escaped —
// in the meta tags and inside the JSON-LD, which is the one that could close
// its own script element.
func TestAHostileTitleIsEscaped(t *testing.T) {
	d := seoDeps()
	ctx := context.Background()
	p, err := d.Projects.Create(ctx, "owner-sub", `</script><img src=x onerror="alert(1)">`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Projects.SetPublic(ctx, p.ID, project.SignOff{By: "owner-sub", AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	body := do(t, NewRouter(d).Handler(), "GET", "/p/"+p.ID, "", "").Body.String()

	if strings.Contains(body, "<img src=x") || strings.Contains(body, `onerror="alert(1)"`) {
		t.Error("a title reached the document as markup")
	}
	if strings.Contains(body, "</script><img") {
		t.Error("a title closed the ld+json element")
	}
	if !strings.Contains(body, "&lt;/script&gt;") {
		t.Error("the title was not escaped into the head at all")
	}
}

// AC8: describing a page never leaves the database. A crawler walking the public
// zone must not become one forge request per URL, so every page still renders
// with a reader that fails every call.
func TestDescribingAPageMakesNoForgeCall(t *testing.T) {
	d := seoDeps()
	d.Reproductions = &fakeRepro{getErr: ErrReproNotFound, transcriptErr: ErrReproNotFound}
	p := describedProject(t, d)
	j := session(t, d, p.ID, "doi:10.1007/3-540-48157-5_9", true)
	h := NewRouter(d).Handler()

	for _, path := range []string{"/", "/registry", "/p/" + p.ID, "/p/" + p.ID + "/s/" + j.ID} {
		w := do(t, h, "GET", path, "", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>") {
			t.Errorf("%s: status %d — a page did not render without the forge", path, w.Code)
		}
	}
}

// The landing and the registry describe the platform rather than one record, and
// carry the structured data that names the site itself.
func TestTheLandingAndRegistryDescribeTheSite(t *testing.T) {
	h := NewRouter(seoDeps()).Handler()

	landing := do(t, h, "GET", "/", "", "").Body.String()
	for _, want := range []string{`"@type":"WebSite"`, `"@type":"Organization"`, `<link rel="canonical" href="https://replichai.test/" />`} {
		if !strings.Contains(landing, want) {
			t.Errorf("the landing is missing %q", want)
		}
	}
	registry := do(t, h, "GET", "/registry", "", "").Body.String()
	for _, want := range []string{`"@type":"CollectionPage"`, `<link rel="canonical" href="https://replichai.test/registry" />`} {
		if !strings.Contains(registry, want) {
			t.Errorf("the registry is missing %q", want)
		}
	}
}

// A description longer than a card will carry is cut at a word, once.
func TestALongDescriptionIsCutAtAWord(t *testing.T) {
	long := strings.Repeat("reproducibility ", 40)
	got := summarize(long, "")
	if len(got) > maxDescription+len("…") {
		t.Errorf("summary is %d bytes, want at most %d", len(got), maxDescription)
	}
	if !strings.HasSuffix(got, "…") || strings.HasSuffix(got, " …") {
		t.Errorf("summary %q does not end in a clean ellipsis", got)
	}
	if summarize("", "the fallback") != "the fallback" {
		t.Error("an empty description did not fall back")
	}
	if summarize("  two\nlines  ", "") != "two lines" {
		t.Error("whitespace was not collapsed")
	}
}

// A paper's identity resolves to where the work lives, per scheme (001).
func TestAPaperKeyResolvesToItsSource(t *testing.T) {
	for _, tc := range []struct{ key, ref, want string }{
		{"doi:10.1145/1", "", "https://doi.org/10.1145/1"},
		{"arxiv:2401.00001", "", "https://arxiv.org/abs/2401.00001"},
		{"url:https://example.org/p.pdf", "", "https://example.org/p.pdf"},
		{"sha256:abc", "https://example.org/dropped.pdf", "https://example.org/dropped.pdf"},
		{"sha256:abc", "a local file", ""},
	} {
		if got := sourceURL(project.Paper{Key: tc.key, Ref: tc.ref}); got != tc.want {
			t.Errorf("sourceURL(%q, %q) = %q, want %q", tc.key, tc.ref, got, tc.want)
		}
	}
}

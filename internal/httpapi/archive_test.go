// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
)

// openZip reads a response body as a zip archive. A byte-count assertion would
// pass on output that only resembles a zip, which is the failure a streamed
// archive can produce.
func openZip(t *testing.T, body []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the response is not a readable zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		_ = rc.Close()
		out[f.Name] = buf.String()
	}
	return out
}

// TestProjectArchive covers 020 AC1, AC2 and AC7: the archive holds every
// session's committed tree under the session that produced it, plus a manifest
// naming the contents, and it opens as a zip.
func TestProjectArchive(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Point of no Undo")
	published := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	// A second session that never published has nothing committed, so it
	// appears as a line in the manifest rather than as an empty directory.
	unpublished, err := d.Jobs.Enqueue(ctx, job.Submission{
		Principal: owner.Sub, Ref: "ref-2", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:2",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := doAs(t, h, "GET", "/projects/"+p.ID+"/archive", "", owner)
	if w.Code != http.StatusOK {
		t.Fatalf("archive = %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Point-of-no-Undo.zip"`) {
		t.Errorf("Content-Disposition = %q, want the project's name", cd)
	}

	files := openZip(t, w.Body.Bytes())
	for _, want := range []string{
		"sessions/" + published.ID + "/replication.json",
		"sessions/" + published.ID + "/README.md",
		"sessions/" + published.ID + "/src/main.py",
		"MANIFEST.txt",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("the archive has no %s; it holds %v", want, slices.Sorted(maps.Keys(files)))
		}
	}
	// Files are organised by session, not merged. Two sessions' REPRODUCIBILITY.md
	// must not collide, which is the distinction 015 restored.
	if body := files["sessions/"+published.ID+"/README.md"]; body != "content of README.md" {
		t.Errorf("README.md = %q, want the committed bytes", body)
	}
	manifest := files["MANIFEST.txt"]
	if !strings.Contains(manifest, "Point of no Undo") || !strings.Contains(manifest, published.ID) {
		t.Errorf("the manifest names neither the project nor its session:\n%s", manifest)
	}
	if !strings.Contains(manifest, unpublished.ID) || !strings.Contains(manifest, "skipped") {
		t.Errorf("the manifest does not account for the session that never published:\n%s", manifest)
	}
	if strings.Contains(manifest, "ERRORS") {
		t.Errorf("the manifest reports errors on a clean archive:\n%s", manifest)
	}
}

// TestArchiveRefusesAProjectWithNothingInIt covers 020 AC3: a project that has
// produced nothing answers a refusal with a reason, not an empty archive.
func TestArchiveRefusesAProjectWithNothingInIt(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Empty")

	w := doAs(t, h, "GET", "/projects/"+p.ID+"/archive", "", owner)
	if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "no_artifacts" {
		t.Errorf("archive of an empty project = %d/%q, want 409/no_artifacts", w.Code, errorCode(w.Body.Bytes()))
	}
}

// TestArchiveFollowsViewRights covers 020 AC4: the check is view rights, not
// ownership. A collaborator downloads the project they work in and a reader
// downloads what was published. A non-member of a private project is answered
// 404.
func TestArchiveFollowsViewRights(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@x.com"}
	stranger := Identity{Sub: "stranger-sub", Email: "s@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Shared")
	_ = publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("invite: %d", w.Code)
	}
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if w := doAs(t, h, "GET", "/projects/"+p.ID+"/archive", "", collab); w.Code != http.StatusOK {
		t.Errorf("a collaborator's download = %d, want 200", w.Code)
	}
	// The project is hidden rather than refused, because a 403 would tell a
	// non-member that the project exists.
	if w := doAs(t, h, "GET", "/projects/"+p.ID+"/archive", "", stranger); w.Code != http.StatusNotFound {
		t.Errorf("a stranger's download of a private project = %d, want 404", w.Code)
	}

	// Once the project is public, anyone signed in may read it, so anyone signed
	// in may download it.
	publishable(t, d, owner.Sub, "ada")
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	if w := doAs(t, h, "GET", "/projects/"+p.ID+"/archive", "", stranger); w.Code != http.StatusOK {
		t.Errorf("a public project's archive = %d for a signed-in reader, want 200", w.Code)
	}
}

// TestPublishedSessionCountMatchesWhatTheArchiveServes checks that the
// download is offered on exactly the projects the archive can serve. A session
// count does not say whether any session published: repo_name marks that
// publishing began, and a project whose every session failed before that has
// sessions and no artifacts. The list therefore carries a separate published
// count.
func TestPublishedSessionCountMatchesWhatTheArchiveServes(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}

	empty, _ := d.Projects.Create(ctx, owner.Sub, "Ran, never published")
	if _, err := d.Jobs.Enqueue(ctx, job.Submission{
		Principal: owner.Sub, Ref: "ref", Kind: job.KindAudit, Project: empty.ID, PaperKey: "arxiv:9",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	full, _ := d.Projects.Create(ctx, owner.Sub, "Published")
	_ = publishInto(t, d.Jobs, owner.Sub, full.ID, "r1")

	byID := map[string]ProjectView{}
	for _, v := range listOf(t, h, "/projects", owner) {
		byID[v.ID] = v
	}
	if v := byID[empty.ID]; v.SessionCount != 1 || v.PublishedSessions != 0 {
		t.Errorf("a project that ran but never published reports %d/%d sessions/published, want 1/0", v.SessionCount, v.PublishedSessions)
	}
	if v := byID[full.ID]; v.PublishedSessions != 1 {
		t.Errorf("a published project reports %d published sessions, want 1", v.PublishedSessions)
	}
	// The count agrees with the endpoint. Zero published sessions is what the
	// archive refuses, so the button and the handler cannot disagree.
	if w := doAs(t, h, "GET", "/projects/"+empty.ID+"/archive", "", owner); errorCode(w.Body.Bytes()) != "no_artifacts" {
		t.Errorf("the archive of a project with no published session = %d/%q, want no_artifacts", w.Code, errorCode(w.Body.Bytes()))
	}
}

// TestArchiveRecordsWhatItCouldNotRead covers 020 AC5: a forge read that fails
// loses its session, not the whole download. The status code has already been
// sent by then, so the failure is recorded inside the archive. A truncated zip
// with a 200 is silently incomplete; one with the failure noted is not.
func TestArchiveRecordsWhatItCouldNotRead(t *testing.T) {
	d := testDeps(allowAll)
	d.Reproductions = &brokenRepro{fakeRepro: &fakeRepro{}, breakOn: "r2"}
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Half readable")
	good := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	bad := publishInto(t, d.Jobs, owner.Sub, p.ID, "r2")

	w := doAs(t, h, "GET", "/projects/"+p.ID+"/archive", "", owner)
	if w.Code != http.StatusOK {
		t.Fatalf("archive = %d, want 200 — one unreadable session must not fail the download", w.Code)
	}
	files := openZip(t, w.Body.Bytes())
	if _, ok := files["sessions/"+good.ID+"/README.md"]; !ok {
		t.Errorf("the readable session is missing; the archive holds %v", slices.Sorted(maps.Keys(files)))
	}
	manifest := files["MANIFEST.txt"]
	if !strings.Contains(manifest, "ERRORS") || !strings.Contains(manifest, bad.ID) {
		t.Errorf("the manifest does not record the failure:\n%s", manifest)
	}
}

// TestArchiveNameFallsBackToTheID checks that a project whose title has no
// character allowed in a filename still downloads. The id is the fallback,
// because a file with no name is worse than one with an unreadable name.
func TestArchiveNameFallsBackToTheID(t *testing.T) {
	if got := archiveName("///", "abc123"); got != "project-abc123" {
		t.Errorf("archiveName(%q) = %q, want the id fallback", "///", got)
	}
	if got := archiveName(`Walk "This" Beam: VR/Height`, "x"); got != "Walk-This-Beam-VR-Height" {
		t.Errorf("archiveName = %q; a filename must carry no quotes or separators", got)
	}
	if got := archiveName(strings.Repeat("a", 200), "x"); len(got) > 80 {
		t.Errorf("archiveName is %d characters, want it capped", len(got))
	}
}

// brokenRepro fails every read of one repository, leaving the others readable.
type brokenRepro struct {
	*fakeRepro
	breakOn string
}

func (b *brokenRepro) Files(ctx context.Context, id string) ([]string, error) {
	if id == b.breakOn {
		return nil, errors.New("the forge is unreachable")
	}
	return b.fakeRepro.Files(ctx, id)
}

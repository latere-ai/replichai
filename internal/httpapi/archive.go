package httpapi

import (
	"archive/zip"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/job"
)

// The project archive (020): one download that hands a reader everything a
// project produced.
//
// The app already proxies committed files one at a time, because the
// repositories are usually private and a reader with legitimate access cannot
// simply clone them. That left no way to get the work out: a project with three
// sessions and forty files each is a hundred and twenty clicks and a hundred and
// twenty loose files with no record of which session each came from. For a
// product whose argument is that a verdict should be checkable, taking the
// evidence away to check it should not be the hard part.

// archiveProject streams a project's committed trees as one zip.
//
// It is written straight to the ResponseWriter rather than built and then sent:
// a project's repositories can be large, and buffering one in memory to learn
// its size before sending buys nothing here — nobody needs a Content-Length on
// this.
//
// Which means the status code is committed before the work is done. Once the
// first byte is out the response is a 200 whatever happens next, so a session
// whose forge read fails is recorded in the archive's own MANIFEST.txt instead.
// A truncated zip with a 200 is silently incomplete; a noted one is not.
func (rt *Router) archiveProject(w http.ResponseWriter, r *http.Request) {
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	// Hidden, not refused — getProject's rule rather than the older member
	// routes': whether a stranger's project exists is not a stranger's business,
	// and a route added now should not inherit that leak. The predicate is the
	// same one every other project-scoped read consults, so a public project is
	// open to any reader and a private one to its members.
	if !rt.mayViewProject(r, proj) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	// The one refusal that can be decided before the first byte, and worth a real
	// status: "this project has produced nothing yet" is a different answer from
	// an empty archive.
	if !anyPublished(sessions) {
		writeError(w, http.StatusConflict, "no_artifacts")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+archiveName(proj.Title, proj.ID)+`.zip"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(w)
	// The manifest is written last (it reports what happened) but read first, so
	// the notes accumulate while the tree is walked.
	notes := &manifest{project: proj.Title, id: proj.ID}
	// Oldest first: the archive reads in the order the work happened, which is the
	// opposite of the order a list of sessions is useful in.
	for _, session := range slices.Backward(sessions) {
		rt.archiveSession(r, zw, session, notes)
	}
	if err := writeEntry(zw, "MANIFEST.txt", []byte(notes.render()), time.Time{}); err != nil {
		log.Printf("archive %s: manifest: %v", proj.ID, err)
	}
	if err := zw.Close(); err != nil {
		// Nothing can be said to the client at this point — the body is already
		// half-sent — so this is for us.
		log.Printf("archive %s: closing the zip: %v", proj.ID, err)
	}
}

// archiveSession writes one session's committed tree under sessions/{id}/,
// recording what it could not read rather than failing the download.
func (rt *Router) archiveSession(r *http.Request, zw *zip.Writer, s job.Job, notes *manifest) {
	if s.RepoName == "" {
		// A session that never reached publishing has nothing committed. It gets a
		// line rather than an empty directory, so the archive's contents and the
		// project's session list can be reconciled.
		notes.skip(s, "no repository — this session never published")
		return
	}
	repo := repoName(s.RepoName)
	files, err := rt.repro.Files(r.Context(), repo)
	if err != nil {
		notes.fail(s, fmt.Sprintf("could not list %s: %v", repo, err))
		return
	}
	sort.Strings(files)
	written := 0
	for _, path := range files {
		raw, err := rt.repro.Artifact(r.Context(), repo, path)
		if err != nil {
			notes.fail(s, fmt.Sprintf("could not read %s in %s: %v", path, repo, err))
			continue
		}
		if err := writeEntry(zw, "sessions/"+s.ID+"/"+path, raw, s.UpdatedAt); err != nil {
			notes.fail(s, fmt.Sprintf("could not write %s: %v", path, err))
			continue
		}
		written++
	}
	notes.session(s, repo, written)
}

// writeEntry puts one file in the archive. Deflate, because a reproduction is
// mostly source and text.
func writeEntry(zw *zip.Writer, name string, body []byte, at time.Time) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	if !at.IsZero() {
		h.Modified = at
	}
	f, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	return err
}

// anyPublished reports whether any session in the project has a repository to
// archive.
func anyPublished(sessions []job.Job) bool {
	for _, s := range sessions {
		if s.RepoName != "" {
			return true
		}
	}
	return false
}

// manifest is the archive's own account of itself: what this is, which sessions
// are in it, and what could not be read. It is the only place a mid-stream
// failure can be reported, since the status code is long gone by then.
type manifest struct {
	project string
	id      string
	lines   []string
	errs    []string
}

func (m *manifest) session(s job.Job, repo string, files int) {
	m.lines = append(m.lines, fmt.Sprintf("sessions/%s  %s  %s  %d file(s)  %s", s.ID, s.Phase, repo, files, s.Ref))
}

func (m *manifest) skip(s job.Job, why string) {
	m.lines = append(m.lines, fmt.Sprintf("sessions/%s  %s  skipped: %s  %s", s.ID, s.Phase, why, s.Ref))
}

func (m *manifest) fail(s job.Job, what string) {
	m.errs = append(m.errs, fmt.Sprintf("sessions/%s: %s", s.ID, what))
}

func (m *manifest) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "replichai project archive\n\nProject: %s\nID: %s\n\nSESSIONS\n", m.project, m.id)
	for _, l := range m.lines {
		b.WriteString("  " + l + "\n")
	}
	if len(m.errs) > 0 {
		// The archive is complete except for these. Saying so is the whole reason
		// this section exists: an incomplete download that looks complete is the
		// failure worth engineering against.
		b.WriteString("\nERRORS\n")
		for _, e := range m.errs {
			b.WriteString("  " + e + "\n")
		}
	}
	return b.String()
}

// unsafeInName is everything a downloaded filename should not carry: path
// separators, quotes (the Content-Disposition header is quoted), and anything a
// shell or a file manager would rather not meet.
var unsafeInName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// archiveName turns a project title into a filename, falling back to the id for
// a title that survives none of it — an untitled project still downloads.
func archiveName(title, id string) string {
	name := strings.Trim(unsafeInName.ReplaceAllString(title, "-"), "-")
	if len(name) > 80 {
		name = strings.Trim(name[:80], "-")
	}
	if name == "" {
		return "project-" + id
	}
	return name
}

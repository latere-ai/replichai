// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// MaxImportBytes bounds one imported record. The evidence itself lives in the
// pushed repository; only the record arrives here. A body larger than this is
// refused with 413 rather than truncated to fit, because a silently shortened
// record is worse evidence than a refused import.
const MaxImportBytes = 1 << 20

// importedSession is the record a terminal sends about a session it ran (045k).
// The locator is deliberately absent: it holds a host path and a process
// handle, which are meaningless here and would leak the operator's home
// directory.
type importedSession struct {
	PaperKey   string        `json:"paper_key"`
	Ref        string        `json:"ref"`
	Runtime    string        `json:"runtime"`
	SessionID  string        `json:"session_id"`
	Harness    string        `json:"harness,omitempty"`
	Sandbox    string        `json:"sandbox,omitempty"`
	Phase      string        `json:"phase"`
	Grade      string        `json:"grade,omitempty"`
	Model      importedModel `json:"model"`
	Usage      importedUsage `json:"usage"`
	Repo       string        `json:"repo,omitempty"`
	Error      string        `json:"error,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}

type importedModel struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type importedUsage struct {
	InputFresh   int64 `json:"input_fresh"`
	InputCached  int64 `json:"input_cached"`
	CacheWrite   int64 `json:"cache_write"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Output       int64 `json:"output"`
}

// importSession files a session somebody ran on their own machine. It reaches
// the store through ImportLocal, which does not touch the credit ledger, because
// the compute was bought from the vendor by whoever ran it.
//
// An imported session is a new session, so a published project refuses it as
// it refuses starting one.
//
// Importing is not publishing. An owner's explicit sign-off is still what
// publishes a session, imported or not.
func (rt *Router) importSession(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForNewWork(w, r)
	if !ok {
		return
	}
	// The body is bounded at registration, where bodyLimits names this route,
	// so a record past MaxImportBytes fails the decode with the error tooLarge
	// recognizes.
	var body importedSession
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		if tooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "record_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	phase := job.Phase(strings.TrimSpace(body.Phase))
	if !phase.Terminal() {
		writeError(w, http.StatusBadRequest, "session_not_terminal")
		return
	}
	repo := strings.TrimSpace(body.Repo)
	if repo != "" && !rt.importableRepo(repo) {
		writeError(w, http.StatusBadRequest, "bad_repo")
		return
	}
	held, ok := rt.paperHeldBy(w, r, proj, body.PaperKey)
	if !ok {
		return
	}
	ref := strings.TrimSpace(body.Ref)
	if ref == "" {
		ref = held.Ref
	}
	imported, err := rt.jobs.ImportLocal(r.Context(), job.Import{
		Principal: principal(r), Project: proj.ID, Ref: ref, PaperKey: held.Key,
		Runtime: strings.TrimSpace(body.Runtime), ExternalID: strings.TrimSpace(body.SessionID),
		Phase: phase, RepoName: repo, Err: body.Error,
		Status:     statusOf(body),
		StartedAt:  body.StartedAt,
		FinishedAt: body.FinishedAt,
	})
	if errors.Is(err, job.ErrImportedElsewhere) {
		writeError(w, http.StatusConflict, "session_elsewhere")
		return
	}
	if err != nil {
		// A published or trashed project and a re-import that would change a
		// finished row are each a 409 here.
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, importAnswer{JobID: imported.ID, Origin: string(imported.Origin), ProjectID: proj.ID})
}

// forgeOwner is implemented by the forge wiring that knows which organization
// the deployment publishes reproductions under.
type forgeOwner interface{ Owner() string }

// publishOrg is the organization this deployment publishes reproductions to:
// the one its forge wiring names, else the default.
func (rt *Router) publishOrg() string {
	if o, ok := rt.repoPub.(forgeOwner); ok && o.Owner() != "" {
		return o.Owner()
	}
	if o, ok := rt.repos.(forgeOwner); ok && o.Owner() != "" {
		return o.Owner()
	}
	return defaultOrg
}

// importableRepo reports whether a repository an imported session names is one
// this deployment could have published: "owner/name" under its organization,
// with a name made of the characters a repository name allows. A later publish or
// withdrawal changes the visibility of every repository a session names, so a
// name outside the organization, or one that is not a plain repository name, is
// not accepted.
func (rt *Router) importableRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !strings.EqualFold(owner, rt.publishOrg()) {
		return false
	}
	if name == "" || len(name) > maxRepoName || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// defaultOrg is the organization reproductions are published under when the
// forge wiring does not name one. It is spelled here because this package must
// not depend on the publishing engine.
const defaultOrg = "open-hai"

// maxRepoName is the longest repository name GitHub accepts.
const maxRepoName = 100

// importAnswer is the answer to an import. Its members are declared in
// sorted order, so a re-push of the same session answers the same bytes.
type importAnswer struct {
	JobID     string `json:"job_id"`
	Origin    string `json:"origin"`
	ProjectID string `json:"project_id"`
}

// statusOf converts the operator's report into the RunStatus every read path
// already understands, so an imported session needs no special case in any
// handler that displays one.
func statusOf(body importedSession) provider.RunStatus {
	status := provider.RunStatus{
		State:         provider.StateDone,
		Runtime:       strings.TrimSpace(body.Runtime),
		Model:         strings.TrimSpace(body.Model.ID),
		ModelProvider: strings.TrimSpace(body.Model.Provider),
		Usage: provider.Usage{
			InputTokens:        body.Usage.InputFresh,
			CachedInputTokens:  body.Usage.InputCached,
			CacheWriteTokens:   body.Usage.CacheWrite,
			CacheWrite5mTokens: body.Usage.CacheWrite5m,
			CacheWrite1hTokens: body.Usage.CacheWrite1h,
			OutputTokens:       body.Usage.Output,
		},
	}
	// The grade arrives as its stable name, which is the only form used on
	// the wire. Grade is a uint8, so a direct conversion would read the
	// name as a code point.
	if name := strings.TrimSpace(body.Grade); name != "" {
		var grade provider.Grade
		if err := grade.UnmarshalJSON([]byte(strconv.Quote(name))); err == nil {
			status.Verdict = &provider.Verdict{Grade: grade}
		}
	}
	return status
}

// paperHeldBy resolves the paper a record is filed against. A session may only
// be filed against a paper the project holds, for the same reason a session may
// only run against one: otherwise a caller could attach anything to a project.
func (rt *Router) paperHeldBy(w http.ResponseWriter, r *http.Request, proj project.Project, key string) (project.Paper, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return project.Paper{}, false
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return project.Paper{}, false
	}
	for _, paper := range papers {
		if paper.Key == key {
			return paper, true
		}
	}
	writeError(w, http.StatusNotFound, "paper_not_found")
	return project.Paper{}, false
}

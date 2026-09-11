// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/notify"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/verdict"
)

// Identity is the authenticated caller as the auth middleware (006c) resolved
// it: the stable subject and the IdP-authenticated email. The gate consumes
// these and nothing else. This package only reads the identity from the
// request; who a principal is and what they may do remain 006c's concern.
//
// Name and Picture are display claims (006m). They are rendered and never
// consulted by any authorisation decision. Both may be empty.
type Identity struct {
	Sub     string
	Email   string
	Name    string
	Picture string
}

// ctxKey is the type of this package's context keys. identityKey carries the
// Identity on the request context.
type ctxKey int

const identityKey ctxKey = iota

// WithIdentity returns a context carrying the authenticated identity.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// IdentityFrom reads the authenticated identity, ok=false when unauthenticated.
func IdentityFrom(r *http.Request) (Identity, bool) {
	id, ok := r.Context().Value(identityKey).(Identity)
	return id, ok
}

// principal reads the authenticated subject, empty when unauthenticated.
func principal(r *http.Request) string {
	id, _ := IdentityFrom(r)
	return id.Sub
}

// routes registers every endpoint through the two registration helpers, so the
// gated and open sets come from one place and cannot drift. The sign-in and /me
// open routes are added by 006c; this package owns the operator set.
func (rt *Router) routes() {
	// A health check sits outside the gate so a load balancer can reach it; the
	// sign-in and /me routes join the open set with 006c.
	rt.openHandle("GET", "/healthz", rt.health)
	// A researcher's page is readable by anyone, signed in or not (006r), with
	// the same openness as the public zone.
	rt.openHandle("GET", "/profiles/{handle}", rt.publicProfile)
	// The public zone, and everything a public project already permits anyone to
	// read (028). mayViewProject has admitted any caller to a public project
	// since 006f; before 028 the gate refused the request before it could. A
	// reader with no stake gets a redacted copy; see the redaction section
	// below.
	//
	// The following stay gated deliberately: GET /jobs, which is scoped to the
	// caller by construction; /jobs/{id}/events, a live stream that no published
	// record needs; the member list, which names people who did not publish
	// themselves; and the archive, which would stream a whole repository to an
	// unauthenticated caller.
	rt.openHandle("GET", "/public", rt.listPublic)
	rt.openHandle("GET", "/projects/{id}", rt.getProject)
	rt.openHandle("GET", "/projects/{id}/papers", rt.listProjectPapers)
	rt.openHandle("GET", "/jobs/{id}", rt.getJob)
	rt.openHandle("GET", "/jobs/{id}/transcript", rt.getSessionTranscript)
	rt.openHandle("GET", "/jobs/{id}/reproduction", rt.getSessionReproduction)
	rt.openHandle("GET", "/jobs/{id}/verdict", rt.getSessionFindings)
	rt.openHandle("GET", "/jobs/{id}/artifacts", rt.listSessionArtifacts)
	rt.openHandle("GET", "/jobs/{id}/artifact", rt.getSessionArtifact)
	// The payment processor's webhook carries no session cookie; its signature is
	// its authentication (022b). It sits outside the operator gate deliberately,
	// and is still enumerated in OpenRoutes so the choice is visible.
	rt.openHandle("POST", "/stripe/webhook", rt.stripeWebhook)
	rt.gatedHandle("GET", "/me/profile", rt.getProfile)
	rt.gatedHandle("PATCH", "/me/profile", rt.saveProfile)
	rt.gatedHandle("POST", "/resolve", rt.resolvePaper)
	rt.gatedHandle("POST", "/projects", rt.createProject)
	rt.gatedHandle("POST", "/projects/{id}/papers", rt.addProjectPaper)
	rt.gatedHandle("DELETE", "/projects/{id}/papers", rt.removeProjectPaper)
	rt.gatedHandle("POST", "/projects/{id}/papers/move", rt.moveProjectPaper)
	rt.gatedHandle("PATCH", "/projects/{id}/papers", rt.editProjectPaper)
	rt.gatedHandle("POST", "/projects/{id}/papers/refresh", rt.refreshProjectPapers)
	rt.gatedHandle("POST", "/projects/{id}/description/draft", rt.draftDescription)
	rt.gatedHandle("POST", "/projects/{id}/sessions", rt.startSession)
	rt.gatedHandle("POST", "/projects/{id}/sessions/import", rt.importSession)
	rt.gatedHandle("POST", "/projects/{id}/repository", rt.provisionRepository)
	rt.gatedHandle("GET", "/models", rt.listModels)
	rt.gatedHandle("PATCH", "/projects/{id}", rt.renameProject)
	rt.gatedHandle("GET", "/projects", rt.listProjects)
	// The literal segment wins over {id} in Go's mux, so the trash screen is
	// reachable at a path that also looks like a project id. router_test asserts
	// it rather than trusting the precedence.
	rt.gatedHandle("GET", "/projects/trash", rt.listTrash)
	rt.gatedHandle("DELETE", "/projects/{id}", rt.trashProject)
	rt.gatedHandle("POST", "/projects/{id}/restore", rt.restoreProject)
	rt.gatedHandle("GET", "/projects/{id}/archive", rt.archiveProject)
	rt.gatedHandle("POST", "/projects/{id}/public", rt.makeProjectPublic)
	rt.gatedHandle("POST", "/projects/{id}/private", rt.withdrawProject)
	rt.gatedHandle("GET", "/me/credits", rt.myCredits)
	rt.gatedHandle("POST", "/me/credits/checkout", rt.startCheckout)
	rt.gatedHandle("POST", "/projects/{id}/credits", rt.allocateCredits)
	rt.gatedHandle("POST", "/projects/{id}/credits/reclaim", rt.reclaimCredits)
	rt.gatedHandle("GET", "/projects/{id}/members", rt.listMembers)
	rt.gatedHandle("POST", "/projects/{id}/members", rt.addMember)
	rt.gatedHandle("DELETE", "/projects/{id}/members/{email}", rt.removeMember)
	rt.gatedHandle("GET", "/jobs", rt.listJobs)
	rt.gatedHandle("POST", "/jobs/{id}/cancel", rt.cancelSession)
	rt.gatedHandle("DELETE", "/jobs/{id}", rt.deleteSession)
	rt.gatedHandle("GET", "/jobs/{id}/events", rt.jobEvents)

	// The sitemap lists the public zone for a crawler that has not followed a
	// link to it (042b). It is registered ahead of the SPA fallback, which would
	// otherwise answer the request with the app.
	rt.openHandle("GET", "/sitemap.xml", rt.sitemapXML)

	// The public app addresses each resolve their subject before the shell is
	// served (042a). They are registered ahead of the fallback below, which they
	// end in. seo.go holds the rules.
	rt.spaRoutes()

	// The frontend shell is public and serves any GET path not matched above,
	// so a deep link loads the app and lets it route (006e). More specific API
	// patterns take precedence; the app's data calls are still gated.
	if rt.spa != nil {
		rt.openHandle("GET", "/", rt.spa.ServeHTTP)
	}
}

// writeReadError maps a ReproReader error to a status: a genuine miss is 404
// and a transport or backend failure is 500, so a cancelled request is never
// reported as not found.
func writeReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrReproNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeError(w, http.StatusInternalServerError, "registry_unavailable")
}

// health is the unauthenticated liveness check.
func (rt *Router) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// The papers a project holds, and starting a session on one (012).

// PaperView is a paper as the project screen reads it: the record, plus what
// has happened to it. The session count and latest phase are joined from the
// job store through the paper key both sides carry. A paper with no sessions is
// the ordinary state of one nobody has started yet.
type PaperView struct {
	project.Paper
	Sessions    int    `json:"sessions"`
	LatestPhase string `json:"latest_phase,omitempty"`
}

// listProjectPapers returns the papers a project is about, each with what has
// been run against it.
func (rt *Router) listProjectPapers(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForRead(w, r)
	if !ok {
		return
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, paperViews(papers, sessions))
}

// paperViews joins each paper to what has been run against it. The counts are
// computed here rather than stored, so a session that failed, was deleted or
// moved cannot leave a number behind that nothing updates.
func paperViews(papers []project.Paper, sessions []job.Job) []PaperView {
	out := make([]PaperView, 0, len(papers))
	for _, p := range papers {
		v := PaperView{Paper: p}
		for _, j := range sessions {
			if j.PaperKey != p.Key {
				continue
			}
			v.Sessions++
			// ListByProject is newest-first, so the first match is the latest.
			if v.LatestPhase == "" {
				v.LatestPhase = string(j.Phase)
			}
		}
		out = append(out, v)
	}
	return out
}

// addProjectPaper records another paper on a project. It starts nothing: since
// 012, adding a paper and running it are two separate actions, and this is the
// first.
func (rt *Router) addProjectPaper(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "missing_url")
		return
	}
	key, err := paper.Key(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unresolvable_paper")
		return
	}
	rec := project.Paper{Key: key, Ref: body.URL, AddedBy: principal(r)}
	switch err := rt.projects.AddPaper(r.Context(), proj.ID, rec); {
	case errors.Is(err, project.ErrPaperExists):
		writeError(w, http.StatusConflict, "paper_exists")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "add_paper_failed")
		return
	}
	added := PaperResult{Ref: body.URL, Key: key}
	// No placeholder is passed: adding a paper to an existing project fills in
	// that paper's title and never changes the project's own name.
	rt.background(func(ctx context.Context) { rt.enrichTitles(ctx, proj.ID, "", []PaperResult{added}) }) //nolint:contextcheck // background hands the closure a fresh context on purpose: r.Context() is cancelled when this response is written, which is when the enrichment starts.
	writeJSON(w, http.StatusCreated, added)
}

// editProjectPaper records a correction to a paper's bibliography (037).
//
// It is the first route that writes a paper field. Before it, the only writer
// was the background enrichment, and a source that got an author's name wrong
// could not be corrected. A correction may be made by any member, the same
// people who may add a paper, and it is permanent, which the store enforces by
// marking every field it writes.
//
// The key travels as a query parameter rather than a path segment, as it does
// for removing and moving: a paper key is `doi:10.1145/3772318.3791265`, and
// the slash in it is not a path separator.
func (rt *Router) editProjectPaper(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return
	}
	var edit project.PaperEdit
	if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := tidyPaperEdit(&edit); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := rt.projects.EditPaper(r.Context(), proj.ID, key, edit)
	switch {
	case errors.Is(err, project.ErrPaperNotFound):
		writeError(w, http.StatusNotFound, "paper_not_found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "edit_paper_failed")
		return
	}
	writeJSON(w, http.StatusOK, PaperView{Paper: p})
}

// The limits a correction is held to. A bibliography is short by nature; these
// exist so a field nobody validates upstream cannot be used to store arbitrary
// text.
const (
	maxPaperField = 500
	maxAuthors    = 100
	// An abstract runs to a few hundred words; this is generous enough for the
	// longest real one and still a bound.
	maxAbstract = 8000
)

// tidyPaperEdit trims what a person typed and refuses what is too long. It
// trims rather than refusing whitespace, because a trailing space in a pasted
// author list is not a mistake worth a 400.
func tidyPaperEdit(e *project.PaperEdit) error {
	for _, f := range []*string{e.Title, e.Venue, e.Volume, e.Issue, e.Pages, e.Publisher} {
		if f == nil {
			continue
		}
		*f = strings.TrimSpace(*f)
		if len(*f) > maxPaperField {
			return errors.New("field_too_long")
		}
	}
	if e.Authors != nil {
		if len(*e.Authors) > maxAuthors {
			return errors.New("too_many_authors")
		}
		out := make([]string, 0, len(*e.Authors))
		for _, a := range *e.Authors {
			a = strings.TrimSpace(a)
			if a == "" {
				continue // An empty row in the editor is not an author.
			}
			if len(a) > maxPaperField {
				return errors.New("field_too_long")
			}
			out = append(out, a)
		}
		*e.Authors = out
	}
	if e.Abstract != nil {
		// An abstract is a paragraph rather than a field: it gets its own, much
		// larger bound, and the same trim.
		*e.Abstract = strings.TrimSpace(*e.Abstract)
		if len(*e.Abstract) > maxAbstract {
			return errors.New("field_too_long")
		}
	}
	if e.Year != nil && *e.Year != 0 && (*e.Year < 1600 || *e.Year > 2200) {
		return errors.New("bad_year")
	}
	return nil
}

// refreshProjectPapers fills in the details of every paper in the project whose
// record is incomplete (037), and reports how many it completed.
//
// An owner uses it for a project that predates the bibliography. It runs the
// same enrichment a fresh add runs, so what a refresh may overwrite, which is
// nothing a person edited and nothing already filled, is decided in one place.
// Requests to the metadata services are serialised: they are free services,
// and a project with twenty papers must not open twenty connections to
// Crossref at once.
func (rt *Router) refreshProjectPapers(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	if rt.resolver == nil {
		writeError(w, http.StatusServiceUnavailable, "no_resolver")
		return
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	filled := 0
	for _, p := range papers {
		if !p.NeedsDetails() {
			continue
		}
		got, err := rt.resolver.Resolve(r.Context(), p.Ref)
		if err != nil {
			continue // A source that cannot answer now does not fail this request.
		}
		changed, err := rt.projects.EnrichPaper(r.Context(), proj.ID, p.Key, got.Title, got.Record)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "enrich_failed")
			return
		}
		if changed {
			filled++
		}
	}
	papers, err = rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Filled int         `json:"filled"`
		Papers []PaperView `json:"papers"`
	}{filled, paperViews(papers, sessions)})
}

// draftDescription writes a description for a project with one model call
// (039), charges the project for it, and returns the text without storing it.
//
// The draft is a suggestion. It is placed in the editor for the owner to read,
// change and save, which keeps 038's field human-written. A draft they discard
// is still charged, because the tokens were spent either way.
//
// The order is: authorisation, pricing, the project's balance, the model call,
// then the charge. Pricing before the call is what guarantees that an unpriced
// model does not draft; otherwise a model with no price would be discovered
// only after the tokens were spent.
func (rt *Router) draftDescription(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	if rt.drafter == nil {
		writeError(w, http.StatusServiceUnavailable, "drafting_unconfigured")
		return
	}
	rates, err := rt.draftRates(r.Context())
	if err != nil {
		// This is a deployment error. The owner is told the deployment cannot
		// draft, and the log records which model has no price for the operator.
		slog.WarnContext(r.Context(), "draft failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "drafting_unpriced")
		return
	}
	if rt.credits != nil {
		available, err := rt.credits.Available(r.Context(), credit.Pot(proj.ID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "credits_read_failed")
			return
		}
		if available <= 0 {
			writeError(w, http.StatusPaymentRequired, "no_credits")
			return
		}
	}
	// One draft at a time per project.
	//
	// The balance check above is read-then-spend, and the spend is recorded
	// after the model has answered, so a hundred concurrent requests would all
	// read the same positive balance and all buy tokens. A session cannot do
	// this, because 011 holds the money before the work, but a draft has no job
	// to hold against, so the bound is on the work instead. Sequential drafts
	// still stop at the balance, because each one reads it after the last was
	// charged.
	if !rt.beginDraft(proj.ID) {
		writeError(w, http.StatusTooManyRequests, "draft_in_flight")
		return
	}
	defer rt.endDraft(proj.ID)

	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	got, err := rt.drafter.Complete(r.Context(), draft.Request{
		System:    draft.DescriptionSystem,
		Prompt:    draft.DescriptionPrompt(proj.Title, draftPapers(papers)),
		MaxTokens: draftMaxTokens,
	})
	if err != nil {
		slog.WarnContext(r.Context(), "draft failed", "project", proj.ID, "error", err)
		// A model that answered with nothing still spent tokens, so the project
		// is charged before the request is refused; otherwise the deployment
		// would fund compute nobody is billed for. Every other failure produced
		// no usage to charge for.
		if errors.Is(err, draft.ErrEmptyDraft) {
			if _, ok := rt.chargeDraft(w, r, proj.ID, rates, got.Usage); !ok {
				return
			}
			writeError(w, http.StatusBadGateway, "drafting_empty")
			return
		}
		writeError(w, http.StatusBadGateway, "drafting_failed")
		return
	}

	cost, ok := rt.chargeDraft(w, r, proj.ID, rates, got.Usage)
	if !ok {
		return
	}
	// A Drafter that returns an empty text with no error is still refused
	// rather than stored: the interface is exported, and only the models in
	// this repository promise ErrEmptyDraft.
	if strings.TrimSpace(got.Text) == "" {
		writeError(w, http.StatusBadGateway, "drafting_empty")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Description string `json:"description"`
		Model       string `json:"model"`
		CostMicro   int64  `json:"cost_micro"`
	}{got.Text, got.Model, cost})
}

// chargeDraft bills a project for the tokens one drafting call spent, and
// reports the cost and whether the request may continue. It runs after every
// call the model answered, whether with text or with nothing, because the
// charge is for the tokens, not for the usefulness of the result.
//
// The charge is idempotent per call: the reference is minted here, so a client
// that retries a request pays twice, because there were two calls, while a
// retried write of one call pays once.
func (rt *Router) chargeDraft(w http.ResponseWriter, r *http.Request, projectID string, rates credit.Rates, usage provider.Usage) (int64, bool) {
	cost := rates.Price(usage)
	if rt.credits == nil || cost <= 0 {
		return cost, true
	}
	ref, mintErr := draftRef(projectID)
	if mintErr != nil {
		writeError(w, http.StatusInternalServerError, "draft_charge_failed")
		return 0, false
	}
	if err := rt.credits.Spend(r.Context(), credit.Pot(projectID), cost, ref, principal(r)); err != nil {
		// The tokens are spent and unaccounted for. That is logged, but it
		// does not change the response: refusing a draft that was written
		// would charge the project nothing and give it nothing, and a draft
		// that was not written is already being refused.
		slog.WarnContext(r.Context(), "draft: charging the project", "project", projectID, "micro_usd", cost, "error", err)
	}
	return cost, true
}

// beginDraft claims the one drafting slot a project has, and reports whether
// it succeeded. The slot is per process, so in a deployment of N replicas it
// bounds concurrent drafts per project to N rather than to one. It is a bound
// rather than a lock, and it limits the cost of a burst to a few cents rather
// than to however many requests the burst contains.
func (rt *Router) beginDraft(projectID string) bool {
	rt.drafting.mu.Lock()
	defer rt.drafting.mu.Unlock()
	if rt.drafting.inFlight == nil {
		rt.drafting.inFlight = map[string]bool{}
	}
	if rt.drafting.inFlight[projectID] {
		return false
	}
	rt.drafting.inFlight[projectID] = true
	return true
}

// endDraft releases the slot.
func (rt *Router) endDraft(projectID string) {
	rt.drafting.mu.Lock()
	defer rt.drafting.mu.Unlock()
	delete(rt.drafting.inFlight, projectID)
}

// draftMaxTokens bounds a draft. A description is a paragraph, and the bound
// keeps the cost of one draft to a fraction of a cent.
const draftMaxTokens = 400

// draftRates returns the rates the configured drafting model is priced at, or
// an error.
//
// It requires an exact entry in the rate card and never uses the card's
// fail-expensive fallback. That fallback exists so an unrecognised session
// model is charged the dearest rates rather than nothing, which is right for a
// run somebody chose and wrong for a local model an operator configured, since
// it would bill Opus rates for a model that costs very little. Without an exact
// price there is no draft.
func (rt *Router) draftRates(ctx context.Context) (credit.Rates, error) {
	if rt.policy == nil {
		return credit.Rates{}, errors.New("no platform settings to price a draft from")
	}
	set, err := rt.policy.Settings(ctx)
	if err != nil {
		return credit.Rates{}, fmt.Errorf("reading settings: %w", err)
	}
	name := rt.drafter.Name()
	rates, ok := set.Rates.Priced(name)
	if !ok {
		return credit.Rates{}, fmt.Errorf("the rate card has no entry for %s", name)
	}
	return rates, nil
}

// draftRef mints the idempotency key for one draft charge.
func draftRef(projectID string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "draft:" + projectID + ":" + hex.EncodeToString(b[:]), nil
}

// draftPapers projects the store's papers onto the fields a prompt needs. It is
// a projection rather than a pass-through so the drafting package never sees a
// project's internals, and so what reaches a third-party endpoint is exactly
// this list and nothing else.
func draftPapers(papers []project.Paper) []draft.Paper {
	out := make([]draft.Paper, 0, len(papers))
	for _, p := range papers {
		out = append(out, draft.Paper{
			Title:   p.Title,
			Ref:     p.Ref,
			Authors: p.Record.Authors,
			Venue:   p.Record.Venue,
			Year:    p.Record.Year,
		})
	}
	return out
}

// SweepPapers fills in the bibliography of every paper in every project whose
// record is incomplete (037 AC8), and reports how many projects it examined and
// how many papers it completed.
//
// It is the platform's own enrichment, for the projects nobody opens: the same
// enrichment the owner's button runs, over every project rather than one. It is
// not an HTTP handler, because the admin router decides who may call it and
// this method decides what happens. It is safe to run again, because enrichment
// is idempotent.
//
// Requests to the metadata services are strictly serialised. They are free
// services, and a sweep is the one place in this codebase that could send them
// a thousand requests in a second.
func (rt *Router) SweepPapers(ctx context.Context) (projects, filled int, err error) {
	if rt.resolver == nil {
		return 0, 0, errors.New("no paper resolver is configured")
	}
	all, err := rt.projects.ListAll(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list projects: %w", err)
	}
	for _, proj := range all {
		if ctx.Err() != nil {
			return projects, filled, ctx.Err()
		}
		papers, err := rt.projects.ListPapers(ctx, proj.ID)
		if err != nil {
			continue // A project removed during the sweep is skipped.
		}
		projects++
		for _, p := range papers {
			if !p.NeedsDetails() || ctx.Err() != nil {
				continue
			}
			got, err := rt.resolver.Resolve(ctx, p.Ref)
			if err != nil {
				continue
			}
			changed, err := rt.projects.EnrichPaper(ctx, proj.ID, p.Key, got.Title, got.Record)
			if err == nil && changed {
				filled++
			}
		}
	}
	return projects, filled, nil
}

// removeProjectPaper removes a paper a project has never run.
//
// Once a paper has a session it stays: the record of what was reproduced is not
// editable, and every paper the 012 migration backfilled has a session by
// definition, so nothing that predates this endpoint can be removed through it.
// Only a lead may remove, matching who may move credits out.
func (rt *Router) removeProjectPaper(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	if !rt.leads(r, proj) {
		writeError(w, http.StatusForbidden, "not_lead")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	for _, j := range sessions {
		if j.PaperKey == key {
			writeError(w, http.StatusConflict, "paper_has_sessions")
			return
		}
	}
	if err := rt.projects.RemovePaper(r.Context(), proj.ID, key); err != nil {
		writeError(w, http.StatusInternalServerError, "remove_paper_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveProjectPaper refiles a paper, and its finished sessions, under another
// project (035).
//
// It is a removal from one project and an addition to the other, so it is
// authorised like both: lead or owner on each end, which is stricter than the
// any-member rule adding follows and the same as the rule removing follows.
// projectForWork resolves the one project in the path, so the destination is
// resolved separately and put through the same three checks: stake, trash, and,
// new here, whether the project is public.
//
// The sessions move because a session whose project no longer holds its paper
// is the orphan 015 was written to fix. They move only when every one of them
// is finished: a live session rewritten into another project would move inside
// jobs_active_paper_project, the partial unique index the destination's paper
// row cannot see, and the worker driving it holds the old project id in memory.
// That is 032's terminal-only rule, for 032's reason.
func (rt *Router) moveProjectPaper(w http.ResponseWriter, r *http.Request) {
	from, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	var body struct {
		PaperKey  string `json:"paper_key"`
		ToProject string `json:"to_project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	key := strings.TrimSpace(body.PaperKey)
	toID := strings.TrimSpace(body.ToProject)
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return
	}
	if toID == "" {
		writeError(w, http.StatusBadRequest, "missing_target")
		return
	}
	if toID == from.ID {
		writeError(w, http.StatusBadRequest, "same_project")
		return
	}
	to, ok := rt.moveTarget(w, r, toID)
	if !ok {
		return
	}
	// Authorisation is checked before state, on both ends. The other order
	// would tell a caller who may not move anything into a project whether it
	// is published: a caller can name any id they have a stake in, so the
	// refusals below would otherwise report its visibility before reporting
	// that they may not act.
	if !rt.leads(r, from) || !rt.leads(r, to) {
		writeError(w, http.StatusForbidden, "not_lead")
		return
	}
	// The move is refused if either project is published. This is read from
	// Visibility, never from the presence of a sign-off: 006o leaves the sign-off
	// on a withdrawn project, so a sign-off proves nothing about current
	// visibility. Moving a paper out of a published project would change what a
	// signed attestation covers; the owner must withdraw first.
	if from.Visibility == project.VisibilityPublic || to.Visibility == project.VisibilityPublic {
		writeError(w, http.StatusConflict, "project_public")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), from.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	for _, j := range sessions {
		if j.PaperKey == key && !j.Phase.Terminal() {
			writeError(w, http.StatusConflict, "paper_running")
			return
		}
	}
	switch err := rt.projects.MovePaper(r.Context(), from.ID, to.ID, key); {
	case errors.Is(err, project.ErrPaperNotFound):
		writeError(w, http.StatusNotFound, "paper_not_found")
	case errors.Is(err, project.ErrPaperExists):
		writeError(w, http.StatusConflict, "paper_exists")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "move_paper_failed")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// moveTarget resolves a move's destination. It does projectForWork's job for a
// project named in the body rather than the path, with its own not-found code
// so the app can say which end of the move was missing.
func (rt *Router) moveTarget(w http.ResponseWriter, r *http.Request, id string) (project.Project, bool) {
	return rt.resolveProject(w, r, id, needWork, false, "target_not_found")
}

// leads reports whether the caller may take a lead's action in this project. An
// owner leads their own by definition; isLead covers a promoted collaborator.
func (rt *Router) leads(r *http.Request, p project.Project) bool {
	return p.Owner == principal(r) || rt.isLead(r, p.ID)
}

// startSession runs one paper the project already holds.
//
// Every refusal a create used to report per paper is now reported here, for one
// paper at a time: the double-trigger guard (409), an empty project pot (402),
// and a key the project does not hold (404). One action gets one answer, and
// the caller is looking at the project whose credits are being spent, which is
// the one screen where an out-of-credits message names something they can act
// on.
//
// The paper key travels in the body, never the path: paper.Key returns values
// such as "doi:10.1145/3544548.3581433" and "url:https://...", so a key spans
// several path segments and would not route.
func (rt *Router) startSession(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	var body struct {
		PaperKey string `json:"paper_key"`
		// Tier is the model tier to run on (031). It is a plain string on the
		// wire, as every wire type here represents a domain enum. When omitted,
		// the caller did not choose and the engine's default applies.
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	// The set of tiers is validated here, at the only boundary a person's choice
	// crosses. The provider adapter passes a preference it does not recognise
	// through as a literal model id, and a model the card cannot name is priced
	// at the dearest rates it holds, so an unchecked string would be both a
	// request for a model that may not exist and a bill at the top of the card.
	tier := credit.Preference(strings.TrimSpace(body.Tier))
	if tier != "" && !tier.Valid() {
		writeError(w, http.StatusBadRequest, "bad_tier")
		return
	}
	key := strings.TrimSpace(body.PaperKey)
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	var held *project.Paper
	for i := range papers {
		if papers[i].Key == key {
			held = &papers[i]
			break
		}
	}
	// A session may only run a paper the project is about. Without this a caller
	// could spend a project's credits on anything at all.
	if held == nil {
		writeError(w, http.StatusNotFound, "paper_not_found")
		return
	}
	j, err := rt.jobs.Enqueue(r.Context(), job.Submission{
		Principal: principal(r), Ref: held.Ref, Kind: job.KindAudit, Project: proj.ID, PaperKey: held.Key,
		Tier: tier,
	})
	if err != nil {
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"job_id": j.ID, "project_id": proj.ID})
}

// need is what a route requires of the caller in a project.
type need int

const (
	// needView means the caller may see the project: it is public, or they
	// own it or are a member of it.
	needView need = iota
	// needWork means the caller may work in it: they are the owner, or a
	// member with a working role, as mayWorkInProject decides.
	needWork
	// needOwner means the caller owns it.
	needOwner
)

// resolveProject is the one check every project-scoped route passes, so the
// rules live in one place and no route can apply a different one:
//
//   - A project that does not exist and a project the caller may not view
//     answer the same 404 with the same body. A caller who may not view a
//     project must not learn whether it exists, and a 403 would tell them.
//   - A project the caller may view but not at the level the route needs is
//     refused with 403 not_owner. They already have access to the project, so
//     the reason may be disclosed.
//   - A project in the trash is readable by its owner and writable by nobody
//     (019). A route that works on a trashed project passes allowTrashed;
//     every other route is refused with 409 project_trashed here, once,
//     rather than in each handler.
//
// notFound is the code the route answers a hidden project with. The codes
// differ by route history and the app keys on them.
func (rt *Router) resolveProject(w http.ResponseWriter, r *http.Request, id string, n need, allowTrashed bool, notFound string) (project.Project, bool) {
	proj, err := rt.projects.Get(r.Context(), id)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, notFound)
		return project.Project{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return project.Project{}, false
	}
	if !rt.mayViewProject(r, proj) {
		writeError(w, http.StatusNotFound, notFound)
		return project.Project{}, false
	}
	switch n {
	case needView:
	case needWork:
		if !rt.mayWorkInProject(r, proj, principal(r)) {
			writeError(w, http.StatusForbidden, "not_owner")
			return project.Project{}, false
		}
	case needOwner:
		if proj.Owner != principal(r) {
			writeError(w, http.StatusForbidden, "not_owner")
			return project.Project{}, false
		}
	}
	if proj.Trashed() && !allowTrashed {
		writeError(w, http.StatusConflict, "project_trashed")
		return project.Project{}, false
	}
	return proj, true
}

// projectForWork loads the project named in the path and refuses a caller who
// may not work in it. It is the one check every paper and session write shares,
// so no route can omit it. A project in the trash is refused too: starting a
// session, adding a paper, moving credits and publishing are all closed by this
// one check rather than by a separate check in each handler.
func (rt *Router) projectForWork(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	return rt.resolveProject(w, r, r.PathValue("id"), needWork, false, "project_not_found")
}

// projectForRead is projectForWork's read counterpart: a public project is open
// to anyone signed in, so listing what a project is about follows the same rule
// as listing its sessions. A trashed project stays readable by its owner.
func (rt *Router) projectForRead(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	return rt.resolveProject(w, r, r.PathValue("id"), needView, true, "project_not_found")
}

// stakeIn reports whether this caller has a stake in a project, meaning they
// are its owner or a collaborator it was shared with. It is the one predicate
// every redaction keys on (028), and the same one that already withholds the
// credit balance, so membership has a single answer per request rather than one
// per field. Reading a public project confers no stake, so a signed-in caller
// with no relation to a project sees exactly what a signed-out one does.
func (rt *Router) stakeIn(r *http.Request, p project.Project) bool {
	return rt.mayWorkInProject(r, p, principal(r))
}

// mayWorkInProject reports whether a caller may open a session in a project:
// its owner, or a collaborator it was shared with. Reading a public project
// does not confer this, because spending a project's credits is not a read.
func (rt *Router) mayWorkInProject(r *http.Request, p project.Project, sub string) bool {
	// An unauthenticated caller is neither an owner nor a member. The read
	// routes are open since 028, so this predicate now answers for
	// unauthenticated callers too, and it decides both what may be written and
	// what may be read of the credits.
	if sub == "" {
		return false
	}
	if p.Owner == sub {
		return true
	}
	if rt.members == nil {
		return false
	}
	id, _ := IdentityFrom(r)
	_, ok, _ := rt.members.Role(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, p.ID)
	return ok
}

// writeEnqueueError maps an Enqueue failure to a status: the double-trigger
// guard is a 409, an empty reference a 400, anything else a 500.
func (rt *Router) writeEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, job.ErrActiveSession):
		writeError(w, http.StatusConflict, "active_session")
	case errors.Is(err, job.ErrNoCredits):
		writeError(w, http.StatusPaymentRequired, "no_credits")
	case errors.Is(err, job.ErrEmptyRef):
		writeError(w, http.StatusBadRequest, "missing_url")
	default:
		writeError(w, http.StatusInternalServerError, "enqueue_failed")
	}
}

// defaultTitle is the offline placeholder title a new project starts with,
// which is the paper reference itself. The worker replaces it with the resolved
// paper title once its Resolve phase has run.
func defaultTitle(url string) string { return strings.TrimSpace(url) }

// resolvePaper previews a reference against the source (006j): it returns the
// resolved identity so the new-project screen shows the real title before
// anything is created. A bad reference answers 400; an unreachable source
// answers 502. The two are kept distinct so the UI can report an unreachable
// source.
func (rt *Router) resolvePaper(w http.ResponseWriter, r *http.Request) {
	if rt.resolver == nil {
		writeError(w, http.StatusNotImplemented, "resolve_unavailable")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "missing_url")
		return
	}
	got, err := rt.resolver.Resolve(r.Context(), body.URL)
	if errors.Is(err, ErrUnresolvable) {
		writeError(w, http.StatusBadRequest, "unresolvable_paper")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "resolve_failed") // The source could not be reached.
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// PaperResult reports the outcome of one reference in a create: the paper it
// was recorded as, or why it was not recorded. Since 012 a create records
// papers and starts nothing, so the only thing that can go wrong per reference
// is the reference itself; there is no enqueue to fail and no credits to run
// out of.
type PaperResult struct {
	Ref   string `json:"ref"`
	Key   string `json:"key,omitempty"`
	Title string `json:"title,omitempty"`
	Error string `json:"error,omitempty"`
}

// createProject creates a project with a chosen title, the papers it is about,
// and seeded collaborators (006j, reshaped by 012).
//
// It records and does not run. Nothing is enqueued and nothing is spent, so an
// account with an empty wallet can name a project, record what it intends to
// reproduce, and invite people to it. The funding-at-create rule used to refuse
// all of this with a 402. The credit check happens once, later, in
// startSession, where there is a project pot on screen to answer it with.
//
// Because nothing is spent there is nothing to compensate for: 006j's rule
// against leaving an empty project, its 422, and the compensating delete were
// all tied to the enqueue and were removed with it. A project with no papers is
// a project nobody has added a paper to yet.
func (rt *Router) createProject(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r)
	if !ok {
		writeError(w, http.StatusForbidden, "not_granted")
		return
	}
	var body struct {
		Title         string   `json:"title"`
		Papers        []string `json:"papers"`
		Collaborators []string `json:"collaborators"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	// A project may start empty (036). Creating one records papers and enqueues
	// nothing (012), so a paper is content rather than the purpose of a create,
	// and the project is useful before it has any. A project cannot start
	// without a name.
	papers := nonEmpty(body.Papers)
	if len(papers) > MaxPapersPerRequest {
		writeError(w, http.StatusBadRequest, "too_many_papers")
		return
	}
	// placeholder is the title a project gets when nobody names it, and the
	// only title enrichment may replace. A title somebody typed is kept.
	title, placeholder := strings.TrimSpace(body.Title), ""
	if title == "" {
		// With no paper there is nothing to name the project after, so a title
		// is required in that case.
		if len(papers) == 0 {
			writeError(w, http.StatusBadRequest, "missing_title")
			return
		}
		placeholder = defaultTitle(papers[0])
		title = placeholder
	}

	proj, err := rt.projects.Create(r.Context(), id.Sub, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_create_failed")
		return
	}

	results := make([]PaperResult, 0, len(papers))
	for _, ref := range papers {
		results = append(results, rt.recordPaper(r, proj.ID, id.Sub, ref))
	}
	// Titles are written after the response: a create must not wait on an
	// external web server, and the papers are already recorded without them.
	rt.background(func(ctx context.Context) { rt.enrichTitles(ctx, proj.ID, placeholder, results) }) //nolint:contextcheck // background hands the closure a fresh context on purpose: r.Context() is cancelled when this response is written, which is when the enrichment starts.

	members := rt.seedCollaborators(r.Context(), proj.ID, id, body.Collaborators)

	writeJSON(w, http.StatusCreated, map[string]any{
		"project_id": proj.ID,
		"title":      title,
		"papers":     results,
		"members":    members,
	})
}

// recordPaper writes one reference onto a project and reports the outcome.
// paper.Key works offline, so a create makes no network calls and cannot
// partly fail on a slow source. A reference that is not a DOI, arXiv ID, or URL
// is the only thing that can be refused here.
func (rt *Router) recordPaper(r *http.Request, projectID, principal, ref string) PaperResult {
	res := PaperResult{Ref: ref}
	key, err := paper.Key(ref)
	if err != nil {
		res.Error = "unresolvable_paper"
		return res
	}
	res.Key = key
	switch err := rt.projects.AddPaper(r.Context(), projectID, project.Paper{Key: key, Ref: ref, AddedBy: principal}); {
	case err == nil, errors.Is(err, project.ErrPaperExists):
		// A repeated reference in one batch is the same paper named twice, not a
		// failure: the project is about it either way.
	default:
		res.Error = "add_paper_failed"
	}
	return res
}

// enrichTitles resolves each recorded paper against its source and writes back
// what it learns: the paper's own title, and the project's title while that is
// still the placeholder it was created with.
//
// It runs after the response, on a context of its own. Blocking a create on N
// network fetches is the partial failure paper.Key was chosen to avoid: a
// four-paper create would wait on the slowest source, and a client that timed
// out would be left with the papers recorded and no response. Nothing here is
// essential, because every title it writes would be written by the first
// session's Resolve phase anyway.
//
// The client's own preview is never written to the record. The new-project
// screen has already resolved these papers for display, but a title stored
// against a project is a record, and only the server writes records.
func (rt *Router) enrichTitles(ctx context.Context, projectID, placeholder string, papers []PaperResult) {
	if rt.resolver == nil {
		return
	}
	titles := make([]string, len(papers))
	var wg sync.WaitGroup
	// The sources are shared, rate-limited services and this is best-effort
	// work: a project with many papers is resolved a few at a time, never all
	// at once. The bound is here, at the one place enrichment fans out.
	slots := make(chan struct{}, maxConcurrentResolves)
	for i := range papers {
		if papers[i].Error != "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			got, err := rt.resolver.Resolve(ctx, papers[i].Ref)
			if err != nil {
				return
			}
			// A source that answered with a bibliography and no title still has
			// something to record.
			if strings.TrimSpace(got.Title) == "" && got.Record.Empty() {
				return
			}
			titles[i] = got.Title
			_, _ = rt.projects.EnrichPaper(ctx, projectID, papers[i].Key, got.Title, got.Record)
		}(i)
	}
	wg.Wait()

	// The project takes the first title that resolved, not necessarily the first
	// paper's: a project whose lead paper sits on an unreachable source should
	// still end up named after something a person can read. It only ever
	// replaces the placeholder, so a project somebody named keeps its name.
	for _, title := range titles {
		if title == "" {
			continue
		}
		if placeholder == "" {
			return
		}
		if p, err := rt.projects.Get(ctx, projectID); err == nil && p.Title == placeholder {
			_ = rt.projects.SetTitle(ctx, projectID, title)
		}
		return
	}
}

// background runs work that outlives the request that scheduled it, on a
// context of its own so a client disconnecting cannot cancel a write that is
// already in progress. The WaitGroup lets a test wait for it.
func (rt *Router) background(f func(context.Context)) {
	rt.bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), backgroundWork)
		defer cancel()
		f(ctx)
	})
}

// WaitBackground blocks until every background task scheduled so far has
// finished. Tests use it; nothing in the serving path does.
func (rt *Router) WaitBackground() { rt.bg.Wait() }

// backgroundWork bounds any after-the-response work. It is generous: the only
// thing running under it is best-effort enrichment nobody is waiting on.
const backgroundWork = 30 * time.Second

// maxConcurrentResolves is how many paper sources enrichment asks at once. The
// sources are Crossref, arXiv and publishers' pages, all shared and all
// rate-limited; a project with a hundred papers must not open a hundred
// connections to them on one request.
const maxConcurrentResolves = 4

// MaxPapersPerRequest bounds how many papers one request may add. A project
// grows one reading list at a time, and a request beyond this is not one.
const MaxPapersPerRequest = 50

// seedCollaborators seeds the owner as lead, which is idempotent, and adds each
// collaborator email as a member on a best-effort basis. It returns the emails
// that were added. A failure to add one collaborator is reported by omission
// and never fails the create.
func (rt *Router) seedCollaborators(ctx context.Context, projectID string, owner Identity, emails []string) []string {
	if rt.members == nil {
		return nil
	}
	added := []string{}
	p := access.Principal{Sub: owner.Sub, Email: owner.Email}
	if err := rt.members.SeedLead(ctx, projectID, p); err != nil {
		return added
	}
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" || strings.EqualFold(email, owner.Email) {
			continue // Blanks and the owner, who is already the lead, are skipped.
		}
		if err := rt.members.Add(ctx, p, projectID, email, access.RoleCollaborator); err == nil {
			added = append(added, email)
		}
	}
	return added
}

// Wallet is one person's own credits and the statement that explains them
// (011). The statement matters because the ledger exists to prevent a balance
// nobody can account for, so the response that shows a number also shows the
// entries that produced it.
type Wallet struct {
	Balance int64         `json:"balance"`
	Entries []walletEntry `json:"entries"`
	// SpreadBps, SpreadFixedMicro and MinTopupMicro are the purchase policy the
	// top-up modal reads (022b): the spread it applies to show the credited
	// amount, and the floor below which it refuses before the processor is
	// called. They are zero when this deployment sells no credits.
	SpreadBps        int64 `json:"spread_bps"`
	SpreadFixedMicro int64 `json:"spread_fixed_micro"`
	MinTopupMicro    int64 `json:"min_topup_micro"`
}

// walletEntry is a ledger entry as the statement reads it: the durable row, plus
// whether the project it names still exists (024b). project_alive is answered
// by the server rather than the ledger, because the ledger keeps no foreign key
// to a project, so only code that can read the projects table can say whether
// a trashed project still exists to link to or a purged one is gone. It is
// false on every entry that names no project, and on one whose project was
// purged.
type walletEntry struct {
	credit.Entry
	ProjectAlive bool `json:"project_alive,omitempty"`
}

// myCredits reports the caller's own wallet. It is always their own: there is
// no way to read somebody else's, and no id to pass.
func (rt *Router) myCredits(w http.ResponseWriter, r *http.Request) {
	if rt.credits == nil {
		writeError(w, http.StatusNotImplemented, "credits_unavailable")
		return
	}
	id, _ := IdentityFrom(r)
	holder := credit.Wallet(id.Email)
	balance, err := rt.credits.Balance(r.Context(), holder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credits_read_failed")
		return
	}
	entries, err := rt.credits.Entries(r.Context(), holder, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credits_read_failed")
		return
	}
	// A row that names a project gets a liveness flag so the client can link a
	// project that still exists and show a purged one as plain text. Alive
	// means the caller can open it: a project they were removed from, or one
	// they do not own that has been trashed, would answer 404 or 403 at
	// /p/{id}, so it is shown as plain text rather than a broken link. It is
	// resolved once per distinct project, since a statement names few, and a
	// failed read leaves the row unlinked rather than failing the wallet.
	alive := map[string]bool{}
	out := make([]walletEntry, len(entries))
	for i, e := range entries {
		we := walletEntry{Entry: e}
		if e.ProjectID != "" {
			live, seen := alive[e.ProjectID]
			if !seen {
				proj, gerr := rt.projects.Get(r.Context(), e.ProjectID)
				live = gerr == nil && rt.mayViewProject(r, proj)
				alive[e.ProjectID] = live
			}
			we.ProjectAlive = live
		}
		out[i] = we
	}
	wallet := Wallet{Balance: balance, Entries: out}
	// The purchase policy is included so the top-up modal can quote a credit and
	// enforce the floor client-side. A failed read leaves it zero rather than
	// failing the wallet, which reports the balance rather than the sale.
	if rt.policy != nil {
		if set, err := rt.policy.Settings(r.Context()); err == nil {
			wallet.SpreadBps, wallet.SpreadFixedMicro, wallet.MinTopupMicro = set.SpreadBps, set.SpreadFixedMicro, set.MinTopupMicro
		}
	}
	writeJSON(w, http.StatusOK, wallet)
}

// allocateCredits moves credits from the caller's wallet into a project's pot.
// Any member may fund a project they work in; the ledger re-checks that rather
// than trusting this route, so a route mounted without a guard still cannot
// move somebody's money.
func (rt *Router) allocateCredits(w http.ResponseWriter, r *http.Request) {
	rt.moveCredits(w, r, false)
}

// reclaimCredits moves credits from a project's pot back to the caller's
// wallet. Only leads may do this, and the ledger enforces it: the pot is
// fungible and does not record who filled it, so taking from it is an exercise
// of authority over the project's credits rather than a refund of one's own.
func (rt *Router) reclaimCredits(w http.ResponseWriter, r *http.Request) {
	rt.moveCredits(w, r, true)
}

// moveCredits is the shared implementation of both directions: decode, refuse
// an invalid amount, move, and answer with the balances the client would
// otherwise have to re-read.
func (rt *Router) moveCredits(w http.ResponseWriter, r *http.Request, back bool) {
	// The project is resolved before anything about this deployment is
	// disclosed: a caller who may not view the project learns neither that it
	// exists nor whether credits are configured. A caller who can see the
	// project still gets the 403 below from the ledger's own membership rule,
	// which is the authority over its credits.
	proj, ok := rt.resolveProject(w, r, r.PathValue("id"), needView, true, "not_found")
	if !ok {
		return
	}
	if rt.credits == nil {
		writeError(w, http.StatusNotImplemented, "credits_unavailable")
		return
	}
	var err error
	var body struct {
		Amount int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	id, _ := IdentityFrom(r)
	p := access.Principal{Sub: id.Sub, Email: id.Email}
	if back {
		err = rt.credits.Reclaim(r.Context(), p, proj.ID, proj.Title, body.Amount)
	} else {
		err = rt.credits.Allocate(r.Context(), p, proj.ID, proj.Title, body.Amount)
	}
	switch {
	case err == nil:
	case errors.Is(err, credit.ErrNotPositive):
		writeError(w, http.StatusBadRequest, "bad_amount")
		return
	case errors.Is(err, credit.ErrInsufficient):
		writeError(w, http.StatusPaymentRequired, "no_credits")
		return
	case errors.Is(err, credit.ErrNotLead):
		writeError(w, http.StatusForbidden, "not_lead")
		return
	case errors.Is(err, credit.ErrNotMember):
		writeError(w, http.StatusForbidden, "not_member")
		return
	default:
		writeError(w, http.StatusInternalServerError, "credits_move_failed")
		return
	}
	// Both balances are returned, because both changed and a client that re-read
	// them separately could show a moment where the credits are in neither.
	wallet, err := rt.credits.Balance(r.Context(), credit.Wallet(id.Email))
	if err != nil {
		// The move succeeded. Reporting a wallet of zero here would be a false
		// zero, which this API must never emit, so the read failure is reported.
		writeError(w, http.StatusInternalServerError, "credits_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":  wallet,
		"project": rt.projectCredits(r, proj),
	})
}

// renameProject renames an owned project (006j), owner-only.
func (rt *Router) renameProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	// Both fields are optional and independent: renaming and describing are two
	// edits made from the same screen, and a client sending one must not have to
	// resend the other. They are pointers rather than strings so that an omitted
	// field and a cleared one stay distinct, because an owner may clear a
	// description.
	var body struct {
		Title       *string `json:"title,omitempty"`
		Description *string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if body.Title == nil && body.Description == nil {
		writeError(w, http.StatusBadRequest, "nothing_to_change")
		return
	}
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "missing_title")
			return
		}
		if err := rt.projects.SetTitle(r.Context(), proj.ID, title); err != nil {
			writeError(w, http.StatusInternalServerError, "rename_failed")
			return
		}
		proj.Title = title
	}
	if body.Description != nil {
		desc := strings.TrimSpace(*body.Description)
		switch err := rt.projects.SetDescription(r.Context(), proj.ID, desc); {
		case errors.Is(err, project.ErrDescriptionTooLong):
			writeError(w, http.StatusBadRequest, "description_too_long")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "describe_failed")
			return
		}
		proj.Description = desc
	}
	writeJSON(w, http.StatusOK, proj)
}

// nonEmpty trims and drops blank entries, preserving order.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// SpendWindowDays is how far back the projects screen's spend tile looks. A
// window needs a stated length or the number means nothing. Thirty days is the
// length the tile is labelled with, so the two cannot drift.
const SpendWindowDays = 30

// ProjectView is a project in a list, carrying a session summary so the list
// shows the status of each project, meaning how many sessions it has, how many
// are running, and the newest phase, without a per-project read on the client.
type ProjectView struct {
	project.Project
	SessionCount int `json:"session_count"`
	Running      int `json:"running"`
	// PublishedSessions is how many of them committed a repository, which is the
	// exact condition GET /projects/{id}/archive answers 409 no_artifacts on
	// (020). The client cannot derive it: a session count says nothing about
	// whether any of them published, so without this the download would be
	// offered on a project that has nothing to archive.
	PublishedSessions int    `json:"published_sessions"`
	LatestPhase       string `json:"latest_phase,omitempty"`
	// Spend30d is what this project's sessions cost in the last thirty days, in
	// micro-USD (021e). The window is on when a session last changed, not when
	// it started, so a run that began five weeks ago and settled yesterday
	// counts as yesterday's spend. A session still running counts what it has
	// spent so far, because the wallet has already committed it.
	//
	// It is a pointer because a reader with no stake in the project does not
	// receive it (028), and an absent figure must not read as a project that
	// spent nothing. Nil means the figure is withheld; zero is a fact.
	Spend30d *int64 `json:"spend_30d,omitempty"`
	// SignerHandle is where the signer's name links (006r). It is resolved on
	// every read from the signer's profile as it is now, and deliberately not
	// snapshotted onto the project: a stored handle would be a second, staler
	// copy of the same person, and 006n's position is that a published
	// attestation reports what was signed, not what a profile says later.
	//
	// The link therefore follows the profile. Somebody who makes theirs private
	// after publishing keeps their attestation intact and loses the link, which
	// is correct because the record was never the profile.
	SignerHandle string `json:"signer_handle,omitempty"`
	// Papers is what this project is a reproduction of (043): every paper it
	// holds, trimmed to a citation. It is a list because the store holds a list
	// and the card must show one; a project of two papers with one citation line
	// under it would attribute the second paper's work to the first.
	//
	// Unlike Spend30d, it is not gated on a stake in the project. A published
	// project's bibliography is part of what publishing published, and the reader
	// this exists for is the anonymous one on the landing page.
	Papers []PaperCitation `json:"papers,omitempty"`
	// OwnerName and OwnerHandle identify the owner in a readable form (018).
	// Project.Owner is an opaque OIDC subject, which identified nobody in a list
	// that now has an Owner column.
	//
	// Both are resolved per read from the owner's profile, for the same reason
	// SignerHandle is: they are a view of who somebody is now, and a stored copy
	// would go stale against the profile that is the actual record. OwnerHandle
	// is set only when that profile is public, so it is safe to link.
	//
	// Neither is ever the owner's e-mail address. A project stores its signer's
	// e-mail already redacted so a full one never reaches a client; putting the
	// owner's on every row a collaborator can read would undo that more widely.
	OwnerName   string `json:"owner_name,omitempty"`
	OwnerHandle string `json:"owner_handle,omitempty"`
	// ModifiedAt is when this project last changed: the newest of its creation,
	// its papers' additions, and its sessions' updates (018). It is derived on
	// every read; see project.Store.ModifiedAt for why it is not a column.
	ModifiedAt time.Time `json:"modified_at"`
}

// listProjects reports every project the caller may work in, their own and the
// ones they were invited to, as one list, most recently modified first (018).
//
// It is one list because ownership is a column rather than a location. A
// project used to appear in a different tab when somebody else had created it,
// so finding work meant first remembering who had created it.
func (rt *Router) listProjects(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r)
	projects, err := rt.projects.ListByOwner(r.Context(), id.Sub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	seen := make(map[string]bool, len(projects))
	for _, p := range projects {
		seen[p.ID] = true
	}
	if rt.members != nil {
		ids, err := rt.members.Projects(r.Context(), id.Sub)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed")
			return
		}
		for _, pid := range ids {
			if seen[pid] {
				// An owner is seeded as their own project's lead, so the membership
				// store answers with projects ListByOwner already returned.
				continue
			}
			p, err := rt.projects.Get(r.Context(), pid)
			if err != nil {
				continue // A membership whose project is gone is skipped.
			}
			seen[pid] = true
			if p.Trashed() {
				// Get answers with trashed projects on purpose (019), so this is the
				// one list read that has to filter them itself. The owner's own half
				// came from ListByOwner, which already has.
				continue
			}
			projects = append(projects, p)
		}
	}
	views, err := rt.projectViews(r, projects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// PaperCitation is one paper as a list reads it: its title and the three fields
// a citation line is made of. It is a projection of paper.Record rather than
// the record itself, because the abstract is paragraphs long and `ref`,
// `added_by` and `edited` are the owner screen's bookkeeping. Six cards on a
// landing page must not carry six abstracts to print six citations.
type PaperCitation struct {
	Title   string   `json:"title,omitempty"`
	Authors []string `json:"authors,omitempty"`
	Venue   string   `json:"venue,omitempty"`
	Year    int      `json:"year,omitempty"`
}

// citations trims a project's papers down to what a list renders.
func citations(papers []project.Paper) []PaperCitation {
	if len(papers) == 0 {
		return nil
	}
	out := make([]PaperCitation, 0, len(papers))
	for _, p := range papers {
		out = append(out, PaperCitation{
			Title:   p.Title,
			Authors: p.Record.Authors,
			Venue:   p.Record.Venue,
			Year:    p.Record.Year,
		})
	}
	return out
}

// projectViews decorates a set of projects for a list with the session summary,
// the modified time, and the owner. It makes three reads for the whole page
// rather than three per row.
//
// The result is most recently modified first, because that is the column the
// list is sorted by, and an order the reader cannot see is one they have to
// work out by eye.
//
// A summary that could not be read fails the list rather than rendering a zero.
// A count of zero sessions is a claim about the project, and a list that
// silently makes a false one is worse than a list that says it could not load.
//
// It takes the request rather than only its context, because the row a reader
// gets depends on whether they have a stake in the project (028): the public
// zone lists other people's work, and the owner's principal and the project's
// spend are not part of what was published.
func (rt *Router) projectViews(r *http.Request, projects []project.Project) ([]ProjectView, error) {
	ctx := r.Context()
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	status, err := rt.jobs.StatusByProjects(ctx, ids)
	if err != nil {
		slog.WarnContext(r.Context(), "projects: summarising sessions", "error", err)
		return nil, err
	}
	modified, err := rt.projects.ModifiedAt(ctx, ids)
	if err != nil {
		slog.WarnContext(r.Context(), "projects: reading modified times", "error", err)
		return nil, err
	}
	// Spend is the one summary that may fail without failing the list. A missing
	// session count would be a false claim about the project; a missing spend
	// figure renders as a dash, which claims nothing, and refusing the whole list
	// because a cost could not be summed would hide the reader's projects over a
	// number.
	spend, err := rt.jobs.SpendSince(ctx, ids, time.Now().AddDate(0, 0, -SpendWindowDays))
	if err != nil {
		slog.WarnContext(r.Context(), "projects: summing spend", "error", err)
		spend = nil
	}
	// The bibliography is read in one call for the whole list (043). It fails
	// without failing the list, as the spend does and for the same reason: a row
	// that cannot say which paper it reproduces is still a row, and a registry
	// that refuses to render because a citation could not be read shows the
	// reader nothing.
	papers, err := rt.projects.PapersByProjects(ctx, ids)
	if err != nil {
		slog.WarnContext(r.Context(), "projects: reading papers", "error", err)
		papers = nil
	}
	out := make([]ProjectView, 0, len(projects))
	for _, p := range projects {
		st := status[p.ID]
		stake := rt.stakeIn(r, p)
		row := p
		if !stake {
			row = p.Redacted()
		}
		v := ProjectView{
			Project:           row,
			SessionCount:      st.Total,
			Running:           st.Running,
			PublishedSessions: st.Published,
			LatestPhase:       string(st.Latest),
			SignerHandle:      rt.signerHandle(ctx, p),
			ModifiedAt:        later(later(p.CreatedAt, modified[p.ID]), st.LatestAt),
			Papers:            citations(papers[p.ID]),
		}
		// The spend is a pointer so absent and zero stay different things: a
		// reader with no stake is told nothing, and one who spent nothing is told
		// exactly that.
		if stake {
			n := spend[p.ID]
			v.Spend30d = &n
		}
		// The owner's name and handle are resolved from p rather than from the
		// redacted row, so a public reader still reads who published this, by the
		// name they chose to publish under. Only the opaque subject is withheld.
		//
		// A reader with no stake gets the name only with a public profile. rt.owner
		// already gates the handle on that and the display name on nothing, which
		// was right while every reader was a collaborator and is not right for
		// anonymous readers: a name somebody kept private stays private, and the
		// signer of a published project is named by the sign-off block regardless.
		v.OwnerName, v.OwnerHandle = rt.owner(ctx, p.Owner)
		if !stake && v.OwnerHandle == "" {
			v.OwnerName = ""
		}
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(a, b ProjectView) int { return b.ModifiedAt.Compare(a.ModifiedAt) })
	return out, nil
}

// later is the newer of two times.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// owner names a project's owner for a list: their profile's display name, and
// their handle when that profile is public. Both are empty for somebody who has
// saved no profile; the client renders that as a dash rather than an opaque
// subject, which would identify nobody.
func (rt *Router) owner(ctx context.Context, principal string) (name, handle string) {
	if rt.profiles == nil || principal == "" {
		return "", ""
	}
	if p, err := rt.profiles.Get(ctx, principal); err == nil {
		name = p.DisplayName
	}
	if h, ok, err := rt.profiles.PrincipalHandle(ctx, principal); err == nil && ok {
		handle = h
		if name == "" {
			name = h
		}
	}
	return name, handle
}

// listPublic is the public zone: every public project, most recently modified
// first, each carrying its owner, so any signed-in user can browse what others
// have published (006f). It decorates through the same path the caller's own
// list does, because the zone lists other people's projects and had the same
// problem of naming an owner by an unreadable subject.
func (rt *Router) listPublic(w http.ResponseWriter, r *http.Request) {
	build := func(ctx context.Context) ([]ProjectView, error) {
		projects, err := rt.projects.ListPublic(ctx)
		if err != nil {
			return nil, err
		}
		return rt.projectViews(r, projects)
	}
	// An anonymous caller has no stake in any project, so every anonymous
	// caller is shown the same list; it is built once per publicTTL rather
	// than once per crawler request. A signed-in caller's list carries their
	// own stake in each row and is built for them.
	var views []ProjectView
	var err error
	if _, signedIn := IdentityFrom(r); signedIn {
		views, err = build(r.Context())
	} else {
		views, err = rt.anonRegistry.get(r.Context(), build)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// getProfile returns the caller's researcher profile (006n). Before they have
// saved one it returns the zero profile, so the settings screen and the publish
// wizard render the same way on first use as on every other.
func (rt *Router) getProfile(w http.ResponseWriter, r *http.Request) {
	p, err := rt.profiles.Get(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// saveProfile writes the caller's researcher profile. The principal comes from
// the session, never the body, so a profile is always its owner's. A malformed
// identifier is refused with its own code, so the UI can point at the field.
// It is a true PATCH: a field absent from the body is left as it was. That did
// not matter while every field was one the settings form always sent, and
// started to matter when 006r added `public`: a client that saved a display
// name without echoing it back would have silently made somebody's page
// private, and with it removed every link a published attestation made to them.
func (rt *Router) saveProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName *string `json:"display_name"`
		Institution *string `json:"institution"`
		Contact     *string `json:"contact_email"`
		ORCID       *string `json:"orcid"`
		Scholar     *string `json:"scholar_url"`
		Handle      *string `json:"handle"`
		Public      *bool   `json:"public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	current, err := rt.profiles.Get(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_read_failed")
		return
	}
	setString(&current.DisplayName, body.DisplayName)
	setString(&current.Institution, body.Institution)
	setString(&current.Contact, body.Contact)
	setString(&current.ORCID, body.ORCID)
	setString(&current.Scholar, body.Scholar)
	setString(&current.Handle, body.Handle)
	if body.Public != nil {
		current.Public = *body.Public
	}
	current.Principal = principal(r)
	saved, err := rt.profiles.Save(r.Context(), current)
	// A handle somebody else holds is a conflict rather than a malformed field:
	// the value is valid but already taken. The UI points at the same input
	// either way, but the status says which kind of problem it is.
	if errors.Is(err, profile.ErrHandleTaken) {
		writeError(w, http.StatusConflict, "handle_taken")
		return
	}
	if code, ok := profileErrorCode(err); ok {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_save_failed")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// setString applies a PATCH field when the body carried one, and leaves the
// existing value alone when it did not.
func setString(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

// profileErrorCode maps a profile validation error to its wire code, and reports
// false for any other error, so a storage failure is never reported as a bad
// field.
func profileErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, profile.ErrInvalidContact):
		return "invalid_contact", true
	case errors.Is(err, profile.ErrInvalidORCID):
		return "invalid_orcid", true
	case errors.Is(err, profile.ErrInvalidScholar):
		return "invalid_scholar", true
	case errors.Is(err, profile.ErrInvalidHandle):
		return "invalid_handle", true
	case errors.Is(err, profile.ErrHandleReserved):
		return "handle_reserved", true
	default:
		return "", false
	}
}

// publicProfile serves a researcher's page: who they say they are, and the
// public work signed under that name (006r).
//
// It is open, with no sign-in, matching the public zone: a reproduction anyone
// can read, signed by somebody anyone can look up. An unknown handle, a private
// profile, and a profile with no handle all answer the same 404, so the endpoint
// cannot be enumerated to learn who holds which name.
func (rt *Router) publicProfile(w http.ResponseWriter, r *http.Request) {
	if rt.profiles == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, err := rt.profiles.ByHandle(r.Context(), r.PathValue("handle"))
	if errors.Is(err, profile.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_read_failed")
		return
	}
	out := profile.PublicProfile{
		Handle:      p.Handle,
		DisplayName: p.DisplayName,
		Institution: p.Institution,
		Contact:     p.Contact,
		ORCID:       p.ORCID,
		Scholar:     p.Scholar,
		Projects:    rt.signedPublicProjects(r.Context(), p.Principal),
	}
	writeJSON(w, http.StatusOK, out)
}

// signedPublicProjects lists the public projects this principal signed. It reads
// the public zone and filters by signer, so a withdrawn project leaves the page
// as soon as it leaves the zone, and there is one source of truth for what is
// public.
func (rt *Router) signedPublicProjects(ctx context.Context, principal string) []profile.PublicSigned {
	out := []profile.PublicSigned{}
	if rt.projects == nil || principal == "" {
		return out
	}
	projects, err := rt.projects.ListPublic(ctx)
	if err != nil {
		return out
	}
	for _, p := range projects {
		if p.SignedOffBy != principal {
			continue
		}
		out = append(out, profile.PublicSigned{ID: p.ID, Title: p.Title, SignedOffAt: p.SignedOffAt})
	}
	return out
}

// maxWithdrawReason bounds the owner's stated reason. It is their own record
// rather than a published notice, so the limit only has to keep an oversized
// body out of the column.
const maxWithdrawReason = 2000

// withdrawProject ends a publication (006o): the project returns to private, the
// retraction is recorded beside the sign-off it retracts rather than erasing it,
// and, on a best-effort basis, every repository the project owns is set private
// on the forge. Only the owner may withdraw. Withdrawing an already private
// project succeeds and changes nothing, so a double-click cannot rewrite when
// the publication ended.
func (rt *Router) withdrawProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > maxWithdrawReason {
		reason = reason[:maxWithdrawReason]
	}
	if err := rt.projects.SetPrivate(r.Context(), proj.ID, project.Withdrawal{
		By:     principal(r),
		Reason: reason,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "withdraw_failed")
		return
	}
	// The forge repositories are closed on a best-effort basis.
	if sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID); err != nil {
		slog.WarnContext(r.Context(), "withdraw: sessions unreadable, repositories left as they are", "project", proj.ID, "error", err)
	} else {
		rt.closeRepos(r.Context(), sessions)
	}
	rt.publicChanged()
	updated, err := rt.projects.Get(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// makeProjectPublic makes a project public, gated by a responsibility sign-off
// (006f). It resolves through ownedProject, so a caller who may not view the
// project gets 404, a collaborator is refused, and a project in the trash
// cannot be published (019). It refuses a project none of whose published
// sessions cleared 002's redistribution gate (409 not_clear), records the
// sign-off with its signer snapshot (006n), marks the project public, and, on
// a best-effort basis, makes the forge repository public when a publisher is
// wired.
func (rt *Router) makeProjectPublic(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		AttestationVersion string `json:"attestation_version"`
		// Name, Institution, Contact, ORCID and Scholar describe the signer as
		// the wizard confirmed them (006n). They are prefilled from the owner's
		// profile in the UI but recorded from this body: what is published is
		// what they signed, not what their profile says later.
		Name        string `json:"signed_off_name"`
		Institution string `json:"signed_off_institution"`
		Contact     string `json:"signed_off_contact"`
		ORCID       string `json:"signed_off_orcid"`
		Scholar     string `json:"signed_off_scholar"`
		// FlipRepo says whether the forge repository is also made public. Absent
		// means yes, which is the common intent of publishing the reproduction, so
		// a GitHub-connected owner opts out by sending false: the in-app artifacts
		// still go public, but the repository stays private.
		FlipRepo *bool `json:"flip_repo"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.AttestationVersion) == "" {
		// The sign-off is required: a publish with no recorded attestation is
		// refused.
		writeError(w, http.StatusBadRequest, "missing_attestation")
		return
	}
	// The signer snapshot is validated with the same rules as the profile, so a
	// malformed identifier cannot reach a public page through the publish path
	// instead of the settings path. Contact is required here and only here,
	// because a result published with no way to ask about it is the case this
	// spec exists to close.
	signer := profile.Profile{
		DisplayName: body.Name,
		Institution: body.Institution,
		Contact:     body.Contact,
		ORCID:       body.ORCID,
		Scholar:     body.Scholar,
	}.Normalize()
	if signer.Contact == "" {
		writeError(w, http.StatusBadRequest, "missing_contact")
		return
	}
	if code, ok := profileErrorCode(signer.Validate()); ok {
		writeError(w, http.StatusBadRequest, code)
		return
	}

	// Publishing puts a name on a result, and 006r requires that the name
	// resolve to a page: the signer needs a public profile with a handle. This
	// is checked before anything is recorded, so a refusal leaves no partial
	// sign-off behind.
	if !rt.signerIsPublic(r.Context(), proj.Owner) {
		writeError(w, http.StatusConflict, "profile_not_public")
		return
	}

	// The project must have a published session whose manifest cleared the gate,
	// and none that did not; a flagged tree bars the project from going public.
	//
	// A refusal names the blocking sessions and the reason for each, because each
	// reason has a different remedy and the owner cannot guess which applies.
	// They all share the 409, because each is a statement about the state of the
	// project rather than about the request.
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	blockers, repos := rt.publishBlockers(r.Context(), sessions)
	if len(blockers) > 0 {
		writeBlockedError(w, http.StatusConflict, headlineBlock(blockers), blockers)
		return
	}
	if len(repos) == 0 {
		// Nothing reached the forge, so there is nothing to make public. This is
		// not a redistribution verdict; no reproduction has been published yet.
		writeBlockedError(w, http.StatusConflict, "nothing_published", nil)
		return
	}

	// The signer's identity is derived from the session, never from the body: the
	// email the identity provider verified, redacted before it is stored, so a
	// published attestation cannot be attributed to somebody who did not sign it
	// and the full address is never put on the wire.
	id, _ := IdentityFrom(r)
	if err := rt.projects.SetPublic(r.Context(), proj.ID, project.SignOff{
		By:                 principal(r),
		AttestationVersion: body.AttestationVersion,
		Name:               signer.DisplayName,
		Institution:        signer.Institution,
		Contact:            signer.Contact,
		ORCID:              signer.ORCID,
		Scholar:            signer.Scholar,
		Email:              profile.RedactEmail(id.Email),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "publish_failed")
		return
	}
	// The forge repository is made public on a best-effort basis unless the
	// owner opted out. The project is already public in-app either way, and a
	// failure here is logged rather than failing the request.
	flipRepo := body.FlipRepo == nil || *body.FlipRepo
	if flipRepo && rt.repoPub != nil {
		for _, repo := range repos {
			if err := rt.repoPub.SetPublic(r.Context(), repo); err != nil {
				slog.WarnContext(r.Context(), "public flip: repository not flipped", "repo", repo, "error", err)
			}
		}
	}
	rt.publicChanged()
	updated, err := rt.projects.Get(r.Context(), proj.ID)
	if err != nil {
		// The publish succeeded and only the read-back failed. That is reported
		// rather than answering with an empty project that suggests nothing
		// changed.
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// signerHandle resolves the public handle of whoever signed a project, empty
// when nobody signed it or their profile is not public. See ProjectView for why
// this is a view and never a stored field.
func (rt *Router) signerHandle(ctx context.Context, p project.Project) string {
	if rt.profiles == nil || p.SignedOffBy == "" {
		return ""
	}
	handle, ok, err := rt.profiles.PrincipalHandle(ctx, p.SignedOffBy)
	if err != nil || !ok {
		return ""
	}
	return handle
}

// signerIsPublic reports whether a principal has a public profile with a
// handle, which is the precondition for publishing (006r).
//
// A deployment with no profile store does not block publishing: the requirement
// is about attribution, and a missing store is not a reason to refuse an owner
// their own decision.
func (rt *Router) signerIsPublic(ctx context.Context, principal string) bool {
	if rt.profiles == nil {
		return true
	}
	_, ok, err := rt.profiles.PrincipalHandle(ctx, principal)
	return err == nil && ok
}

// BlockReason is why one session stops its project from going public. It is a
// closed set: each value is a different fact about the session, with a
// different remedy for the owner.
type BlockReason string

const (
	// BlockUnfinished means the session has a repository but no manifest. The run
	// never finalized: it failed, was cancelled, or is still running. Nothing is
	// wrong with its content; there is no content yet.
	BlockUnfinished BlockReason = "unfinished"
	// BlockUnreadable means the manifest could not be read. A forge that did not
	// answer is the platform's problem rather than the owner's, and it is
	// temporary.
	BlockUnreadable BlockReason = "unreadable"
	// BlockNotClear means the manifest was read and 002's redistribution review
	// is not clear. It is the only reason that concerns what the reproduction
	// contains; PublishBlocker.Review carries the review state.
	BlockNotClear BlockReason = "not_clear"
)

// PublishBlocker names one session that prevents a project from going public,
// so the owner can be directed to it rather than left to search.
//
// Reason and Review are plain strings on the wire, projected from the typed
// enums behind them, because the wire is a boundary and does not redeclare the
// domain types.
type PublishBlocker struct {
	SessionID string `json:"session_id"`
	Repo      string `json:"repo,omitempty"`
	Ref       string `json:"ref,omitempty"` // The paper the session is a run of, for naming it on screen.
	Reason    string `json:"reason"`
	Review    string `json:"review,omitempty"` // The manifest's redistribution review, when one was read.
}

// headlineBlock picks the code for a refusal that lists several blockers: the
// one the owner has to act on takes precedence over one they can only wait
// out. A flagged tree needs a decision, an unfinished run needs time, and an
// unreadable manifest needs only a retry, so they rank in that order.
func headlineBlock(blockers []PublishBlocker) string {
	seen := map[BlockReason]bool{}
	for _, b := range blockers {
		seen[BlockReason(b.Reason)] = true
	}
	switch {
	case seen[BlockNotClear]:
		return "not_clear"
	case seen[BlockUnfinished]:
		return "sessions_unfinished"
	default:
		return "manifest_unreadable"
	}
}

// publishBlockers reports what stops a project going public, and the
// repositories to make public when nothing does.
//
// It reads every session rather than stopping at the first blocked one. A
// project with three blocked sessions used to refuse three times without naming
// any, so fixing one produced the identical refusal.
//
// A session with no repository is not a blocker: it never began publishing, so
// there is nothing of it to make public. A project where no session has one
// yields no blockers and no repositories, which tells the caller there is
// nothing to publish.
func (rt *Router) publishBlockers(ctx context.Context, sessions []job.Job) ([]PublishBlocker, []string) {
	var blockers []PublishBlocker
	var repos []string
	for _, s := range sessions {
		if s.RepoName == "" {
			continue
		}
		block := func(reason BlockReason, review string) {
			blockers = append(blockers, PublishBlocker{
				SessionID: s.ID,
				Repo:      repoName(s.RepoName),
				Ref:       s.Ref,
				Reason:    string(reason),
				Review:    review,
			})
		}
		repro, err := rt.repro.Get(ctx, repoName(s.RepoName))
		if errors.Is(err, ErrReproNotFound) {
			block(BlockUnfinished, "")
			continue
		}
		if err != nil {
			// This fails closed, as before, but reports that the manifest could not
			// be read rather than reporting it as flagged.
			slog.WarnContext(ctx, "publish gate: manifest unreadable", "repo", s.RepoName, "error", err)
			block(BlockUnreadable, "")
			continue
		}
		if !repro.CanPublish {
			block(BlockNotClear, repro.Review)
			continue
		}
		repos = append(repos, repoName(s.RepoName))
	}
	return blockers, repos
}

// ownedProject resolves the project named in the path for a route only its
// owner may use. Ownership is the authority behind every member route (006f),
// because membership is owner-driven. Nothing about a project in the trash
// changes (019), neither its name, its members nor its visibility, so a trashed
// project is refused; the trash routes resolve through trashableProject
// instead.
func (rt *Router) ownedProject(w http.ResponseWriter, r *http.Request) (project.Project, Identity, bool) {
	id, _ := IdentityFrom(r)
	proj, ok := rt.resolveProject(w, r, r.PathValue("id"), needOwner, false, "not_found")
	return proj, id, ok
}

// trashableProject resolves a project for the trash routes (019): the owner
// alone may remove or restore one, and the one they act on may already be in
// the trash.
func (rt *Router) trashableProject(w http.ResponseWriter, r *http.Request) (project.Project, Identity, bool) {
	id, _ := IdentityFrom(r)
	proj, ok := rt.resolveProject(w, r, r.PathValue("id"), needOwner, true, "not_found")
	return proj, id, ok
}

// trashProject moves a project to the trash, or, with ?purge=1, removes it
// permanently (019).
//
// The two share one route because they are the same operation at different
// depths, and the depth enforces the order: a purge is refused on a project
// that is not in the trash, so the reversible step cannot be skipped.
func (rt *Router) trashProject(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.trashableProject(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("purge") == "1" {
		rt.purgeProject(w, r, proj)
		return
	}
	// A running session would keep spending this project's credits after its
	// owner stopped watching it, so running sessions must be stopped first.
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	for _, s := range sessions {
		if !s.Phase.Terminal() {
			writeError(w, http.StatusConflict, "sessions_running")
			return
		}
	}
	// A publication ends only with a recorded retraction (006o). Deleting does
	// not skip the record, so the withdrawal happens first and through the same
	// path an ordinary withdrawal takes.
	if proj.Visibility == project.VisibilityPublic {
		if err := rt.projects.SetPrivate(r.Context(), proj.ID, project.Withdrawal{By: id.Sub, Reason: withdrawnByDeletion}); err != nil {
			writeError(w, http.StatusInternalServerError, "withdraw_failed")
			return
		}
		rt.closeRepos(r.Context(), sessions)
	}
	if err := rt.projects.Trash(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "trash_failed")
		return
	}
	rt.publicChanged()
	w.WriteHeader(http.StatusNoContent)
}

// withdrawnByDeletion is the reason recorded against a publication that ended
// because its project was deleted. It is the owner's record of why, and the one
// case where the reason is not in their own words.
const withdrawnByDeletion = "the project was deleted"

// purgeProject removes a trashed project permanently: its sessions, its papers,
// and the row itself.
//
// It does not remove the repositories those sessions published. They are closed
// on the forge, exactly as a withdrawal closes them, because a repository the
// agent authored is evidence, and this product does not destroy evidence. A
// purge ends the app's claim on it.
func (rt *Router) purgeProject(w http.ResponseWriter, r *http.Request, proj project.Project) {
	if !proj.Trashed() {
		writeError(w, http.StatusConflict, "not_trashed")
		return
	}
	// Credits outlive the project that held them unless somebody reclaims them.
	// A purge that dropped a funded pot would leave a balance nobody can account
	// for, which is what the ledger exists to prevent.
	if rt.credits != nil {
		balance, err := rt.credits.Balance(r.Context(), credit.Pot(proj.ID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "credits_read_failed")
			return
		}
		if balance != 0 {
			writeError(w, http.StatusConflict, "credits_remaining")
			return
		}
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	rt.closeRepos(r.Context(), sessions)
	// The sessions go first: jobs.project_id references the project row and the
	// foreign key does not cascade, so the other order fails at the database.
	if err := rt.jobs.DeleteByProject(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "purge_failed")
		return
	}
	if err := rt.projects.Delete(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "purge_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// closeRepos makes every repository a project's sessions published private.
//
// It is best-effort by design: the project is already private in this app, and
// a forge outage must not leave an owner unable to withdraw. Every repository
// the project owns is closed, not only those a publish made public, because
// nothing records which those were and closing an already private repository
// costs nothing. The repositories come from the sessions directly rather than
// from publishBlockers, because a project is often withdrawn precisely because
// its tree stopped being clear, and that must not keep the repository open.
//
// One helper serves both ways a publication ends, a withdrawal (006o) and a
// delete (019), because they close exactly the same set for the same reasons,
// and two copies would drift.
func (rt *Router) closeRepos(ctx context.Context, sessions []job.Job) {
	if rt.repoPub == nil {
		return
	}
	for _, s := range sessions {
		if s.RepoName == "" {
			continue
		}
		if err := rt.repoPub.SetPrivate(ctx, repoName(s.RepoName)); err != nil {
			slog.WarnContext(ctx, "delete: repository not closed", "repo", s.RepoName, "error", err)
		}
	}
}

// restoreProject takes a project back out of the trash. Only the owner may
// restore, and restoring a project that was never in the trash succeeds.
func (rt *Router) restoreProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.trashableProject(w, r)
	if !ok {
		return
	}
	if err := rt.projects.Restore(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "restore_failed")
		return
	}
	rt.publicChanged()
	w.WriteHeader(http.StatusNoContent)
}

// listTrash serves the trash screen: the caller's own trashed projects, most
// recently trashed first. Only the caller's own, because only an owner can
// trash a project.
func (rt *Router) listTrash(w http.ResponseWriter, r *http.Request) {
	projects, err := rt.projects.ListTrashed(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	views, err := rt.projectViews(r, projects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	// projectViews sorts on the modified time, which is the column the projects
	// list shows. This screen shows the deleted time, so it sorts on that: a
	// project trashed today belongs at the top however long ago it was last
	// worked in.
	slices.SortStableFunc(views, func(a, b ProjectView) int {
		if a.DeletedAt == nil || b.DeletedAt == nil {
			if b.DeletedAt == nil {
				return -1
			}
			return 1
		}
		return b.DeletedAt.Compare(*a.DeletedAt)
	})
	writeJSON(w, http.StatusOK, views)
}

// listMembers returns a project's contributors, owner-only.
func (rt *Router) listMembers(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	members, err := rt.members.List(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	if members == nil {
		members = []access.Member{}
	}
	writeJSON(w, http.StatusOK, members)
}

// addMember invites a collaborator into a project by email. Only the owner may
// invite. The owner is seeded as the project's lead, which is idempotent, so the
// membership store's own lead check is satisfied and project ownership remains
// the single authority. Inviting an email admits nobody to the platform: they
// still need a platform grant.
//
// It then notifies them (006p). The mail is deliberately best-effort: the
// membership is the durable record, created and authorised here, and an outage
// at a mail provider must not appear to the owner as a failure to share their
// work. A message that was not sent is logged and can be sent again by inviting
// the same person once more.
func (rt *Router) addMember(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.Email) == "" {
		writeError(w, http.StatusBadRequest, "missing_email")
		return
	}
	role := access.RoleCollaborator
	if body.Role == "lead" {
		role = access.RoleLead
	}
	if err := rt.members.SeedLead(r.Context(), proj.ID, access.Principal{Sub: id.Sub, Email: id.Email}); err != nil {
		writeError(w, http.StatusInternalServerError, "member_add_failed")
		return
	}
	// Membership is checked before the add, so an existing member can be told
	// apart from a new one: re-inviting an existing member is a no-op the person
	// should not be notified of twice, while re-inviting somebody who was
	// removed is a real invitation and gets one.
	already := rt.isMember(r.Context(), proj.ID, body.Email)
	if err := rt.members.Add(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, proj.ID, body.Email, role); err != nil {
		writeError(w, http.StatusInternalServerError, "member_add_failed")
		return
	}
	if !already && rt.notifier != nil {
		if err := rt.notifier.ProjectInvitation(r.Context(), body.Email, proj.ID, proj.Title, inviterName(id)); err != nil {
			notify.LogFailure("project invitation", body.Email, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// isMember reports whether an email is already a member of a project. A store
// that cannot be read answers false, because the cost of being wrong is one
// extra message, whereas the other way it would be a silently missing
// invitation.
func (rt *Router) isMember(ctx context.Context, projectID, email string) bool {
	members, err := rt.members.List(ctx, projectID)
	if err != nil {
		return false
	}
	for _, m := range members {
		if strings.EqualFold(m.Email, strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}

// inviterName is the name the invitation gives the person who sent it: their
// display name when the identity provider gave one, their address otherwise. It
// is never the opaque subject, which would mean nothing to a reader.
func inviterName(id Identity) string {
	if n := strings.TrimSpace(id.Name); n != "" {
		return n
	}
	return id.Email
}

// removeMember drops a collaborator by email, owner-only.
func (rt *Router) removeMember(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	if err := rt.members.SeedLead(r.Context(), proj.ID, access.Principal{Sub: id.Sub, Email: id.Email}); err != nil {
		writeError(w, http.StatusInternalServerError, "member_remove_failed")
		return
	}
	if err := rt.members.Remove(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, proj.ID, r.PathValue("email")); err != nil {
		writeError(w, http.StatusInternalServerError, "member_remove_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SessionView is a session as this reader may read it (028).
//
// The two fields shadow job.Job's own at a shallower depth, so encoding/json
// takes these and the embedded ones never reach the wire. Both are empty for a
// reader with no stake in the session's project, and omitted rather than sent
// as zero: a public reader is entitled to the evidence a published reproduction
// rests on, but `"cost": 0` on a session that cost three dollars would be a
// false number, which this codebase does not put on the wire. For the owner and
// a collaborator they carry what job.Job holds, including a genuine zero for a
// session that has spent nothing yet.
type SessionView struct {
	job.Job
	Principal string `json:"principal,omitempty"`
	Cost      *int64 `json:"cost,omitempty"`
}

// sessionView projects one session for a reader, keeping the withheld fields
// exactly when they have a stake in the project it belongs to.
func sessionView(j job.Job, stake bool) SessionView {
	v := SessionView{Job: j}
	if stake {
		cost := j.Cost
		v.Principal, v.Cost = j.Principal, &cost
	}
	return v
}

// sessionViews is sessionView over a list.
func sessionViews(js []job.Job, stake bool) []SessionView {
	out := make([]SessionView, 0, len(js))
	for _, j := range js {
		out = append(out, sessionView(j, stake))
	}
	return out
}

// ProjectDetail is one project with its sessions, newest first.
type ProjectDetail struct {
	Project  project.Project `json:"project"`
	Sessions []SessionView   `json:"sessions"`
	// MaySubmit reports whether this caller may open a session here: its owner
	// or a collaborator, never a reader who reached it through the public zone
	// (006l). The client cannot derive this, because it does not know membership
	// and a public project is readable by everyone.
	MaySubmit bool `json:"may_submit"`
	// SignerHandle is where the signer's name links; see ProjectView.
	SignerHandle string `json:"signer_handle,omitempty"`
	// Credits is what this project holds and what it may still commit (011). It
	// is reported only to somebody who may work here: a reader of a public
	// project sees the research, not who paid for it.
	Credits *ProjectCredits `json:"credits,omitempty"`
}

// ProjectCredits is a project's credits as a member reads them: what the pot
// holds, what is still free to commit, and how much a session needs to start.
//
// Balance and Available differ by the sessions running now, each of which holds
// the reserve. That difference is what a member needs to see when a submit is
// refused on a project that appears to have credits.
//
// The overdraft floor is deliberately not included, because an allowance
// somebody can read is one they plan around.
type ProjectCredits struct {
	Balance      int64 `json:"balance"`
	Available    int64 `json:"available"`
	StartReserve int64 `json:"start_reserve"`
	// MayReclaim reports whether this caller may take credits back out. Only a
	// lead may: the pot is fungible and does not record who filled it, so
	// reclaiming is authority over the project's credits rather than a refund of
	// one's own. The client cannot derive this, because it does not know roles.
	MayReclaim bool `json:"may_reclaim"`
}

// projectCredits reads a project's credits for a caller who may work in it, and
// reports nothing for anyone else. A read failure is not fatal: the project
// still renders, without its balance.
func (rt *Router) projectCredits(r *http.Request, proj project.Project) *ProjectCredits {
	if rt.credits == nil || !rt.mayWorkInProject(r, proj, principal(r)) {
		return nil
	}
	out := ProjectCredits{MayReclaim: rt.isLead(r, proj.ID)}
	balance, err := rt.credits.Balance(r.Context(), credit.Pot(proj.ID))
	if err != nil {
		slog.WarnContext(r.Context(), "credits: reading the balance", "project", proj.ID, "error", err)
		return nil
	}
	available, err := rt.credits.Available(r.Context(), credit.Pot(proj.ID))
	if err != nil {
		slog.WarnContext(r.Context(), "credits: reading the available balance", "project", proj.ID, "error", err)
		return nil
	}
	out.Balance, out.Available = balance, available
	if rt.reserve != nil {
		if n, err := rt.reserve(r.Context()); err == nil {
			out.StartReserve = n
		}
	}
	return &out
}

// isLead reports whether the caller leads this project, which is the role a
// reclaim needs. An owner is seeded as its lead, so this covers them too.
func (rt *Router) isLead(r *http.Request, projectID string) bool {
	if rt.members == nil {
		return false
	}
	id, _ := IdentityFrom(r)
	role, ok, err := rt.members.Role(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, projectID)
	return err == nil && ok && role >= access.RoleLead
}

// mayViewProject is the one predicate every project- and session-scoped read
// consults (006f): a public project is open to anyone, an owner sees their own,
// and a collaborator sees one they were invited to. Visibility lives on the
// project, so everything under it, including sessions, transcripts and history,
// inherits this decision; there is no second, unscoped published surface.
func (rt *Router) mayViewProject(r *http.Request, p project.Project) bool {
	id, _ := IdentityFrom(r)
	// A trashed project is visible to its owner alone (019), whatever its
	// visibility column still says. Checking it here rather than in each read
	// closes the sessions, transcripts and artifacts underneath it in one place.
	// This predicate is the decision everything below a project inherits, and a
	// delete that left those readable would not be a delete.
	if p.Trashed() {
		return id.Sub != "" && p.Owner == id.Sub
	}
	if p.Visibility == project.VisibilityPublic {
		return true
	}
	// An unauthenticated caller owns nothing. Without this check, an anonymous
	// read of a project whose owner column is empty would match on "" == "", and
	// the reads 028 opened are exactly where that would be reachable from the
	// internet.
	if id.Sub == "" {
		return false
	}
	if p.Owner == id.Sub {
		return true
	}
	if rt.members == nil {
		return false
	}
	_, ok, _ := rt.members.Role(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, p.ID)
	return ok
}

// viewableSession resolves a session and the project it belongs to, and checks
// that the caller may view that project, so a session read is scoped exactly
// like its project. Otherwise it writes 404 and returns ok=false, hiding a
// session the caller may not see rather than revealing that it exists.
func (rt *Router) viewableSession(w http.ResponseWriter, r *http.Request, id string) (job.Job, bool) {
	j, _, ok := rt.viewableSessionIn(w, r, id)
	return j, ok
}

// viewableSessionIn is viewableSession returning the project as well, for
// callers that must decide what the reader may see of the session and not only
// whether they may see it (028).
func (rt *Router) viewableSessionIn(w http.ResponseWriter, r *http.Request, id string) (job.Job, project.Project, bool) {
	j, err := rt.jobs.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return job.Job{}, project.Project{}, false
	}
	p, err := rt.projects.Get(r.Context(), j.ProjectID)
	if err != nil || !rt.mayViewProject(r, p) {
		writeError(w, http.StatusNotFound, "not_found")
		return job.Job{}, project.Project{}, false
	}
	return j, p, true
}

// getProject returns a project and its sessions. A project the caller may not
// view answers 404.
func (rt *Router) getProject(w http.ResponseWriter, r *http.Request) {
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	// A project the caller may not view answers 404 rather than 403: a private
	// project reads as not found to anyone but its owner and collaborators.
	if !rt.mayViewProject(r, proj) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	if sessions == nil {
		sessions = []job.Job{}
	}
	// A reader with no stake reads the reproduction and not the wallet behind it
	// (028): the project without its owning principal, and each session without
	// who submitted it or what it cost. Only the payload is redacted; every
	// predicate below still runs against `proj`, the unredacted project.
	stake := rt.stakeIn(r, proj)
	shown := proj
	if !stake {
		shown = proj.Redacted()
	}
	writeJSON(w, http.StatusOK, ProjectDetail{
		Project:      shown,
		Sessions:     sessionViews(sessions, stake),
		MaySubmit:    !proj.Trashed() && stake,
		SignerHandle: rt.signerHandle(r.Context(), proj),
		Credits:      rt.projectCredits(r, proj),
	})
}

// listJobs reports the caller's own jobs, newest first. The scope is the
// authenticated principal alone; a caller never sees another principal's jobs.
// The gate guarantees the request is authenticated, so an empty principal,
// which cannot occur past the gate, lists nothing.
func (rt *Router) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := rt.jobs.ListByPrincipal(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	if jobs == nil {
		jobs = []job.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// Transcript is a session's trajectory and token usage (006h): the parsed
// provider.Event sequence the agent produced, and what the run consumed. Usage
// is nil when it was never recorded, as for a historical backfill, and never a
// fabricated zero.
type Transcript struct {
	Events []TranscriptEvent `json:"events"`
	Usage  *provider.Usage   `json:"usage"`
	// Live reports that these events came from an in-flight run's live stream, so
	// the client keeps polling for more.
	Live bool `json:"live"`
}

// TranscriptEvent is one event as the viewer reads it: the neutral event, plus
// the token usage the backend recorded against that one request (006s).
//
// Usage is derived on the read path from the event's backend-original payload
// and is nil for an event that carried none. Most events carry none, and an
// absent number is a fact about the run rather than a gap to fill, which is
// 006h's rule applied per event. It is deliberately not a field on
// provider.Event, because that type is the schema of the committed
// transcript.jsonl (005), and a published artifact's shape does not change to
// serve a viewer.
type TranscriptEvent struct {
	provider.Event
	Usage *provider.Usage `json:"usage,omitempty"`
}

// getSessionTranscript serves a session's transcript as structured events plus
// its usage (006h). A finished session reads its committed transcript.jsonl
// through the forge; a session still in flight reads the backend's live event
// stream directly, so its trajectory can be inspected in real time and not only
// after it publishes. Only a session with neither a repository nor a live run
// answers 409 not_ready, and the `live` flag tells the client to keep polling.
func (rt *Router) getSessionTranscript(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	var events []provider.Event
	live := false
	switch {
	case !j.Phase.Terminal():
		// The session is still in flight. Its committed transcript does not exist
		// yet, because it is written at publish, and a repository created up front
		// for the live mount (006i) must not be mistaken for one. The backend's
		// live event stream is read by the observed run id; until that id is
		// recorded the session is not ready rather than missing, and the client
		// keeps polling.
		if j.Status.RunID == "" || rt.live == nil {
			writeError(w, http.StatusConflict, "not_ready")
			return
		}
		evs, err := rt.live.Events(r.Context(), j.Status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "live_transcript_unavailable")
			return
		}
		events = evs
		live = true
	case j.RepoName != "":
		// A finished session reads its committed transcript.jsonl through the forge.
		raw, err := rt.repro.Transcript(r.Context(), repoName(j.RepoName))
		if err != nil {
			writeReadError(w, err) // 404 for a genuine miss, 500 for a transport failure.
			return
		}
		events, err = parseTranscriptEvents(raw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "transcript_malformed")
			return
		}
	case j.Status.RunID != "" && rt.live != nil:
		// A finished session with no committed repository, such as a server run
		// that never published, still shows its trajectory through the backend
		// session's events.
		evs, err := rt.live.Events(r.Context(), j.Status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "live_transcript_unavailable")
			return
		}
		events = evs
	default:
		// There is no live run and no committed transcript, so the session is
		// not ready rather than missing.
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	// Each event is paired with its own usage and the session total is taken
	// from the same pass (006s); this also drops the raw payloads, which are the
	// bulk of a transcript. The session's recorded usage still takes precedence
	// for the total when it has one, because it is what the run itself observed,
	// and the derived sum stands in for a historical backfill that never recorded
	// any. Only when neither exists is usage nil, meaning not recorded, and it
	// is never a fabricated zero.
	shown, derived, ok := attachUsage(events)
	var usage *provider.Usage
	switch {
	case j.Status.Usage.Total() > 0:
		u := j.Status.Usage
		usage = &u
	case ok:
		usage = &derived
	}
	writeJSON(w, http.StatusOK, Transcript{Events: shown, Usage: usage, Live: live})
}

// repoName strips an "owner/" prefix from a repository path, so a session's
// recorded "open-hai/name" resolves to the "name" the forge source keys on.
func repoName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

// getSessionReproduction serves a session's manifest summary (006f): the
// verdict, the redistribution review, and the reproduction metadata, read from
// the committed repository. Scoped exactly like the transcript.
func (rt *Router) getSessionReproduction(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	repro, err := rt.repro.Get(r.Context(), repoName(j.RepoName))
	if err != nil {
		writeReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repro)
}

// getSessionFindings serves the session's verdict.json: the boundary table, the
// hidden decisions, the scorecard, and what the authors released (021a).
//
// It is deliberately not folded into /jobs/{id}/reproduction. That route serves
// the manifest, which is the system's statement, whereas these are the agent's
// claims; mixing them would blur the distinction the attestation model rests
// on.
//
// A session with no findings answers 404 with a reason rather than an empty
// body: a run that predates the file and a run whose file failed validation are
// different facts, and the reader is told which.
func (rt *Router) getSessionFindings(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	raw, err := rt.repro.Artifact(r.Context(), repoName(j.RepoName), verdict.File)
	if err != nil {
		if errors.Is(err, ErrReproNotFound) {
			writeError(w, http.StatusNotFound, "no_findings")
			return
		}
		writeReadError(w, err)
		return
	}
	problems, err := verdict.Validate(raw)
	if err != nil {
		writeError(w, http.StatusNotFound, "findings_unreadable")
		return
	}
	if len(problems) > 0 {
		// This repeats the refusal finalize made: a file that does not validate is
		// not served as if it did, and the reasons are included so the pane can
		// say which rule it broke rather than only that it is unavailable.
		msgs := make([]string, 0, len(problems))
		for _, p := range problems {
			msgs = append(msgs, p.String())
		}
		writeJSONError(w, http.StatusUnprocessableEntity, "findings_invalid", msgs)
		return
	}
	v, err := verdict.Parse(raw)
	if err != nil {
		writeError(w, http.StatusNotFound, "findings_unreadable")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// listSessionArtifacts serves the session's repository file tree for the in-app
// artifact browser (006f). It is a pass-through to the forge, in the style of
// OSF, that works even when the forge repository stays private.
func (rt *Router) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	files, err := rt.repro.Files(r.Context(), repoName(j.RepoName))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry_unavailable")
		return
	}
	if files == nil {
		files = []string{}
	}
	slices.Sort(files)
	writeJSON(w, http.StatusOK, map[string][]string{"files": files})
}

// getSessionArtifact serves one committed file's bytes as text. The path is a
// query parameter; the forge scopes reads to the one repository, so a path can
// only reach that repository's committed tree.
func (rt *Router) getSessionArtifact(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		writeError(w, http.StatusBadRequest, "missing_path")
		return
	}
	// The path names a file inside one repository, and the reader builds a forge
	// URL from it. Anything that could escape that repository is refused here,
	// where the string arrives (028): the route is open, so this is a URL an
	// anonymous caller composes, and the forge read runs under the platform's
	// own credential.
	if !repoRelative(path) {
		writeError(w, http.StatusBadRequest, "bad_path")
		return
	}
	raw, err := rt.repro.Artifact(r.Context(), repoName(j.RepoName), path)
	if err != nil {
		writeReadError(w, err)
		return
	}
	// Images are served with a real image type so the browser renders them;
	// everything else is served as plain text, and never as text/html, because a
	// committed .html file must not execute on this origin. nosniff stops the
	// browser from re-sniffing the type.
	//
	// The content type is not enough on its own. An SVG is a document as well as
	// a picture: opened directly in a tab it runs any <script> it carries, on
	// this origin, against whoever opened the link, and nosniff cannot help
	// because image/svg+xml is the declared type rather than a guessed one.
	// These bytes are an agent's commit in a repository, so they are as
	// untrusted as the markdown the client already cleans. The sandbox
	// directive addresses this: with no allow-scripts the document cannot
	// execute, and with no allow-same-origin it is placed in an opaque origin
	// with no access to this one. default-src 'none' stops it fetching anything
	// of its own, including its stylesheets, so an SVG opened directly renders
	// unstyled. That is the hostile path rather than the one the app uses, and
	// the trade is acceptable.
	//
	// The header goes on every artifact response rather than only on the svg
	// branch, so a type added to artifactContentType later inherits the guard.
	// It does not affect the app's own rendering: a response CSP is not applied
	// as a document policy when the bytes arrive through <img src>, which is how
	// the client shows a committed image.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Type", artifactContentType(path))
	w.WriteHeader(http.StatusOK)
	// G705: repoRelative has already refused anything that could leave the one
	// repository, and the response is typed, nosniffed and sandboxed above, so
	// these bytes cannot execute as a document on this origin.
	_, _ = w.Write(raw) //nolint:gosec // G705: the response is sandboxed by CSP and never typed text/html, so committed bytes cannot script this origin.
}

// repoRelative reports whether a path names a file inside a repository and
// nothing else.
//
// It is deliberately strict: no absolute path, no `.` or `..` segment, no
// backslash, no percent sign, and no `?` or `#`. The percent rule stops a
// double-encoded `%252e%252e`: the query decoder unescapes once, so a surviving
// `%2e%2e` would be passed on and decoded again by whoever reads it. The `?`
// and `#` rules exist because the reader inserts this string into a forge URL:
// a `?` would start a query on the contents API, where `ref` picks which branch
// or commit to read, and a `#` would truncate the URL to the repository root.
// Neither is a character in a committed filename. A committed artifact with a
// percent in its name is not worth the ambiguity.
func repoRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\%?#`) {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// artifactContentType maps a committed file's extension to a safe content type:
// a real image type for images, plain text for everything else.
func artifactContentType(path string) string {
	switch strings.ToLower(path[strings.LastIndex(path, ".")+1:]) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "svg":
		return "image/svg+xml"
	default:
		return "text/plain; charset=utf-8"
	}
}

// parseTranscriptEvents parses the newline-delimited provider.Event stream
// committed as transcript.jsonl (005). A blank line is skipped; a malformed
// line fails the parse, so a corrupt transcript answers 500 rather than leaving
// a silent gap.
func parseTranscriptEvents(raw []byte) ([]provider.Event, error) {
	events := []provider.Event{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e provider.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// eventUsage reads only the adapter-normalised Usage field. Vendor wire payloads
// are evidence rather than an application protocol, so they are not parsed here.
func eventUsage(e provider.Event) (provider.Usage, bool) {
	if e.Usage == nil {
		return provider.Usage{}, false
	}
	return *e.Usage, true
}

// attachUsage pairs every event with its own usage and returns the session total
// beside it, so a session whose usage was never recorded still shows real
// numbers. ok is false when no event carries usage, in which case the caller
// reports the usage as not recorded rather than a fabricated zero.
//
// Both come from one pass by design: the total is the sum of exactly the
// per-event numbers the viewer displays, so the two can never disagree (006s).
// It also drops each event's raw payload, which is the bulk of a transcript and
// which nothing downstream of this function reads.
func attachUsage(events []provider.Event) ([]TranscriptEvent, provider.Usage, bool) {
	out := make([]TranscriptEvent, len(events))
	var total provider.Usage
	found := false
	for i, e := range events {
		te := TranscriptEvent{}
		if u, ok := eventUsage(e); ok {
			found = true
			total.Add(u)
			te.Usage = &u
		}
		e.Raw = nil
		e.Usage = nil
		te.Event = e
		out[i] = te
	}
	return out, total, found
}

// getJob reports a session's status and phase, scoped to who may view its
// project.
func (rt *Router) getJob(w http.ResponseWriter, r *http.Request) {
	j, proj, ok := rt.viewableSessionIn(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sessionView(j, rt.stakeIn(r, proj)))
}

// cancelSession stops a running session. Only the owner of the session's project
// may stop it: a collaborator is refused with 403, and a caller who may not view
// the project gets 404, as on every session route. It returns the resulting job;
// stopping an already finished session is a no-op.
func (rt *Router) cancelSession(w http.ResponseWriter, r *http.Request) {
	// The session is resolved as on every other session route: a session the
	// caller may not view answers 404.
	j, proj, ok := rt.viewableSessionIn(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if proj.Owner != principal(r) {
		writeError(w, http.StatusForbidden, "not_owner")
		return
	}
	if rt.canceler == nil {
		writeError(w, http.StatusNotImplemented, "cancel_unavailable")
		return
	}
	if err := rt.canceler.Cancel(r.Context(), j.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "cancel_failed")
		return
	}
	updated, err := rt.jobs.Get(r.Context(), j.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteSession removes one session from the app (032).
//
// It exists because a session that began publishing and never finished holds a
// repository with no manifest, and publishBlockers blocks on exactly that, so
// before 032 a cancelled or failed run made its project permanently
// unpublishable, with no action in the product to clear it. This is that
// action.
//
// It is a soft delete, and it closes the repository rather than destroying it:
// 019's rule, unchanged here, is that a repository the agent authored is
// evidence. What is removed is the project's claim on the run, not the run.
func (rt *Router) deleteSession(w http.ResponseWriter, r *http.Request) {
	j, err := rt.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		// A session already deleted reads as missing, which is what makes a
		// repeated delete a 404 rather than a second close of the repository.
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	proj, err := rt.projects.Get(r.Context(), j.ProjectID)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	// This is the 019 rule applied to a session: a collaborator is refused with
	// 403, and a caller who may not view the project gets 404, so the session's
	// existence is not disclosed.
	if proj.Owner != principal(r) {
		if !rt.mayViewProject(r, proj) {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			writeError(w, http.StatusForbidden, "not_owner")
		}
		return
	}
	if proj.Trashed() {
		writeError(w, http.StatusConflict, "project_trashed")
		return
	}
	// A session still in flight cannot be deleted. This keeps the delete button
	// on a blocker row from ending a publish that is merely slow: a session
	// legitimately in the publishing phase raises the identical unfinished
	// blocker as one that failed there. It must be stopped first with POST
	// /jobs/{id}/cancel.
	if !j.Phase.Terminal() {
		writeError(w, http.StatusConflict, "session_running")
		return
	}
	// The repository is closed before the delete. The other order could lose
	// the repository name to a failed write and leave an open repository that
	// nothing references.
	rt.closeRepos(r.Context(), []job.Job{j})
	if err := rt.jobs.Delete(r.Context(), j.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

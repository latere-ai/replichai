// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package httpapi is the HTTP server that wraps the audit engine: submit a
// paper, watch a job, read a result. It translates HTTP to the job model and
// back and adds no engine logic. Every handler depends on an interface, such as
// the job store, a registry reader, or the gate, and never on internal/audit,
// its method, or a provider adapter, so the whole surface is tested against
// fakes with no database and no network (006a).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/seo"

	"latere.ai/x/pay"
)

// TopupPolicy supplies the purchase spread and floor that the checkout route
// applies and the wallet shows in the top-up modal (022b). It is satisfied by
// platform.Store and is narrow so the router does not depend on the whole
// policy store to price one purchase.
type TopupPolicy interface {
	Settings(ctx context.Context) (platform.Settings, error)
}

// Drafter is the model that writes a draft, as this package needs it: a
// completion and the id it is priced under. It is satisfied by draft.Model and
// deliberately restated here so the HTTP layer depends on the interface rather
// than on the package that calls a vendor.
type Drafter interface {
	Complete(ctx context.Context, r draft.Request) (draft.Result, error)
	Name() string
}

// Route is one registered endpoint, method and pattern, so the gated set is
// enumerable for 006c's test.
type Route struct {
	Method  string
	Pattern string
}

// Gate decides whether a request may reach an operator route. This package
// mounts it and decides which routes it covers; 006c supplies the policy of who
// passes. The default is fail-closed: an unconfigured server admits nobody.
type Gate interface {
	// Allow reports whether the request may proceed, and a machine-readable
	// reason to return when it may not, such as "not_granted".
	Allow(r *http.Request) (ok bool, reason string)
}

// GateFunc adapts a function to a Gate.
type GateFunc func(r *http.Request) (bool, string)

// Allow calls the function.
func (f GateFunc) Allow(r *http.Request) (bool, string) { return f(r) }

// DenyAll is the fail-closed default gate: it admits nobody, so a server wired
// without a real gate cannot accidentally ship its operator routes open.
var DenyAll Gate = GateFunc(func(*http.Request) (bool, string) { return false, "not_granted" })

// JobStore is the part of the job store the handlers need: enqueue a
// submission and read one back. Depending on this interface rather than a
// concrete store keeps engine construction in the composition root and out of
// the handlers, which is what the no-engine-logic dependency test enforces.
type JobStore interface {
	Enqueue(ctx context.Context, s job.Submission) (job.Job, error)
	// ImportLocal files a session that was executed on somebody's own
	// machine (045k). It never reaches the credit ledger.
	ImportLocal(ctx context.Context, in job.Import) (job.Job, error)
	Get(ctx context.Context, id string) (job.Job, error)
	ListByPrincipal(ctx context.Context, principal string) ([]job.Job, error)
	ListByProject(ctx context.Context, projectID string) ([]job.Job, error)
	StatusByProjects(ctx context.Context, ids []string) (map[string]job.ProjectStatus, error)
	// SpendSince sums each project's session costs inside a window (021e).
	SpendSince(ctx context.Context, ids []string, since time.Time) (map[string]int64, error)
	// DeleteByProject removes a project's sessions. A permanent project delete
	// calls it before the project row is removed (019).
	DeleteByProject(ctx context.Context, projectID string) error
	// Delete soft-deletes one session from the app (032). It clears a run that
	// began publishing and never finished, which would otherwise permanently
	// block its project from going public.
	Delete(ctx context.Context, id string) error
}

// Canceler stops a running session. It is the worker, which runs as one
// process with one replica; cancelling interrupts the run in flight and marks
// it cancelled.
type Canceler interface {
	Cancel(ctx context.Context, id string) error
}

// LiveTranscript reads a run's event stream from the backend by its run id. This
// is the live trajectory of a session still in flight, before it has published
// a committed transcript. It is an interface so the handlers do not depend on
// the provider adapter.
type LiveTranscript interface {
	Events(ctx context.Context, status provider.RunStatus) ([]provider.Event, error)
}

// ProjectStore is the part of the project store the handlers need: create a
// project, read one back, list an owner's, and the compensating delete when a
// project's first session fails to enqueue (006g).
type ProjectStore interface {
	Create(ctx context.Context, owner, title string) (project.Project, error)
	Get(ctx context.Context, id string) (project.Project, error)
	ListByOwner(ctx context.Context, owner string) ([]project.Project, error)
	ListPublic(ctx context.Context) ([]project.Project, error)
	// Trash, Restore and ListTrashed implement the trash (019): move a project
	// out of every list, restore it, and list what is in the trash.
	Trash(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	ListTrashed(ctx context.Context, owner string) ([]project.Project, error)
	// ModifiedAt is this store's half of a project's modified time (018).
	ModifiedAt(ctx context.Context, ids []string) (map[string]time.Time, error)
	Delete(ctx context.Context, id string) error
	SetPublic(ctx context.Context, id string, s project.SignOff) error
	// SetPrivate withdraws a publication (006o), recording the retraction without
	// erasing the sign-off it ends.
	SetPrivate(ctx context.Context, id string, w project.Withdrawal) error
	// SetTitle renames a project. It is used to name a project at creation and
	// by the rename endpoint (006j). The worker also enriches a placeholder
	// title through it.
	SetTitle(ctx context.Context, id, title string) error
	SetDescription(ctx context.Context, id, description string) error
	// AddPaper and ListPapers manage the papers a project is about (012). A
	// paper is recorded before anything runs against it and outlives every
	// session that does, so papers are stored with the project rather than
	// derived from the job store.
	AddPaper(ctx context.Context, projectID string, p project.Paper) error
	ListPapers(ctx context.Context, projectID string) ([]project.Paper, error)
	// PapersByProjects is the same read for a list of projects in one call
	// (043). The registry says what each project on it is a reproduction of, and
	// the route that serves it is called without signing in.
	PapersByProjects(ctx context.Context, ids []string) (map[string][]project.Paper, error)
	EnrichPaper(ctx context.Context, projectID, key, title string, rec paper.Record) (bool, error)
	ListAll(ctx context.Context) ([]project.Project, error)
	EditPaper(ctx context.Context, projectID, key string, edit project.PaperEdit) (project.Paper, error)
	RemovePaper(ctx context.Context, projectID, key string) error
	// MovePaper refiles a paper, and the sessions that are runs of it, under
	// another project (035). It is one call because the paper and its runs must
	// move together.
	MovePaper(ctx context.Context, fromID, toID, key string) error
}

// ProfileStore stores the researcher profile (006n), the identity a sign-off is
// prefilled from. It is only ever the caller's own: every handler passes the
// principal from the session, never one from a request body, so this interface
// needs no authorisation of its own.
type ProfileStore interface {
	Get(ctx context.Context, principal string) (profile.Profile, error)
	Save(ctx context.Context, p profile.Profile) (profile.Profile, error)
	// ByHandle resolves the public page anyone may read (006r).
	ByHandle(ctx context.Context, handle string) (profile.Profile, error)
	// PrincipalHandle resolves the handle a sign-off links to its signer by,
	// and is the precondition the public flip checks. It resolves through the
	// profile as it is now, never through a handle snapshotted onto the project,
	// which would be a second, staler copy of the same person.
	PrincipalHandle(ctx context.Context, principal string) (string, bool, error)
}

// PaperResolver previews a paper reference against the source: it resolves the
// reference to its identity so the new-project screen can show the real title
// before anything is created (006j). It is an interface so the handlers do not
// depend on internal/paper; the composition root implements it over
// paper.Resolve and the fetcher. ErrUnresolvable means a bad reference, which
// answers 400; any other error means a source that could not be reached, which
// answers 502. The UI relies on the distinction.
type PaperResolver interface {
	Resolve(ctx context.Context, ref string) (ResolvedPaper, error)
}

// ResolvedPaper is a reference's previewed identity: the offline key and the
// metadata the lookup found.
type ResolvedPaper struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Scheme string `json:"scheme"`
	Slug   string `json:"slug"`
	// Record is the paper's bibliography as the source declared it (037). The
	// preview shows it, and the record a project stores is written from it.
	Record paper.Record `json:"record,omitzero"`
}

// ErrUnresolvable is returned by a PaperResolver for a reference that is not a
// DOI, arXiv ID, or URL. It answers 400, distinct from an unreachable source,
// which answers 502.
var ErrUnresolvable = errors.New("unresolvable paper reference")

// MembershipStore is the part of the project-membership store (006c) the
// members API needs. The handler authorises on project ownership and seeds the
// owner as the project's lead, so the store's own lead check is satisfied
// without a second authority model.
type MembershipStore interface {
	List(ctx context.Context, project string) ([]access.Member, error)
	Add(ctx context.Context, byLead access.Principal, project, email string, r access.Role) error
	Remove(ctx context.Context, byLead access.Principal, project, email string) error
	SeedLead(ctx context.Context, project string, lead access.Principal) error
	// Role resolves a caller's role in a project, binding the membership on
	// first login. It is the collaborator half of the view predicate.
	Role(ctx context.Context, p access.Principal, project string) (access.Role, bool, error)
	// Projects lists the project ids a principal is a member of, which is the
	// "shared with me" list.
	Projects(ctx context.Context, principalSub string) ([]string, error)
}

// Notifier reaches a person who is not in the app (006p). It is used to invite
// somebody to a project and to tell an account holder that a reversed payment
// froze their account. Both concern a person who is not present, so both must
// be delivered outside the app.
//
// It is an interface rather than *notify.Notifier so httpapi tests against a
// fake, and so a deployment can wire nothing at all.
type Notifier interface {
	ProjectInvitation(ctx context.Context, to, projectID, projectName, inviter string) error
	// AccountFrozen tells a person that a reversed payment put their account in
	// view-only mode, and that a top-up lifts it (022d).
	AccountFrozen(ctx context.Context, to string) error
}

// RepoPublisher changes a forge repository's visibility. It is the optional
// half of making a project public (006f) and of withdrawing it again (006o). It
// runs with the server token, never in the sandbox. It is nil when no forge is
// wired, as in a local run or tests, in which case a project changes visibility
// in-app without touching a repository.
type RepoPublisher interface {
	SetPublic(ctx context.Context, repo string) error
	SetPrivate(ctx context.Context, repo string) error
}

// ReproReader is the read interface over the derived registry (003) and the
// transcript (005). It presents what the forge and the manifests already hold;
// no handler caches a reproduction in its own shape.
//
// A genuine miss must be reported as ErrReproNotFound; any other error is a
// transport or backend failure and becomes a 500, so a cancelled request or a
// forge outage is never reported as not found.
//
// It is no longer a user-facing list. The project is the unit of visibility
// (006f), so Get and Transcript serve internal checks only, namely the publish
// gate's manifest-clear check and the session transcript reader, and never an
// unscoped organisation-wide listing.
type ReproReader interface {
	Get(ctx context.Context, id string) (Reproduction, error)
	Transcript(ctx context.Context, id string) ([]byte, error)
	// Files lists a repository's committed artifact paths, and Artifact reads
	// one. They back the in-app file tree (006f), a forge pass-through that works
	// even when the repository itself stays private.
	Files(ctx context.Context, id string) ([]string, error)
	Artifact(ctx context.Context, id, path string) ([]byte, error)
}

// ErrReproNotFound is the sentinel a ReproReader returns for a genuine miss.
var ErrReproNotFound = errors.New("reproduction not found")

// Reproduction is the presented form of one published reproduction. Its fields
// are defined here; it is a view over 003's manifest, never a stored copy.
type Reproduction struct {
	ID           string `json:"id"`
	Repo         string `json:"repo"`
	Title        string `json:"title"`
	Verdict      string `json:"verdict"`
	Created      string `json:"created,omitempty"`
	MethodDigest string `json:"method_digest,omitempty"`
	Review       string `json:"review,omitempty"` // The 002 redistribution review: clear or excluded.
	// CanPublish reports whether the manifest cleared 002's redistribution gate,
	// that is, `review == "clear"`. It is the precondition the public flip
	// enforces (006f).
	CanPublish bool `json:"can_publish"`
}

// Deps are what a Router is built from. All are interfaces, so tests supply
// fakes.
type Deps struct {
	Jobs          JobStore
	Projects      ProjectStore
	Members       MembershipStore
	Profiles      ProfileStore
	Reproductions ReproReader
	RepoPublisher RepoPublisher
	// Repos provisions a repository for a session run on somebody else's
	// machine (045l). It is nil when the deployment cannot mint a scoped token,
	// in which case the route reports so and the terminal keeps its own
	// credential.
	Repos    RepoProvisioner
	Canceler Canceler
	Live     LiveTranscript
	Resolver PaperResolver
	Gate     Gate
	// Credits is the ledger a new project is funded from (011). Nil leaves a
	// project unfunded at creation, which is what an unmetered offline run
	// needs.
	Credits credit.Store
	// Reserve is how much a new project is funded with when the request names
	// no amount. It is the same amount the enqueue gate will then require.
	Reserve credit.Reserve
	// Payments opens a hosted checkout and verifies its webhook (022b). Nil
	// leaves the checkout route reporting payments_unconfigured, so a
	// deployment that does not sell credits still runs.
	Payments pay.Provider
	// Platform supplies the purchase spread and floor. Nil leaves the checkout
	// route and the wallet quote at zero, which is what an offline run needs.
	Platform TopupPolicy
	// Models resolves a tier to the model a run would get, so the tier list can
	// be priced (031). It is satisfied by provider.Provider. Nil lists the tiers
	// with no prices, which is all an offline run with no backend can report.
	Models credit.ModelResolver
	// Drafter writes a project's description when somebody asks for one (039).
	// Nil means a deployment that does not draft: the route answers 503 and the
	// app offers no control, so descriptions are typed by hand.
	Drafter Drafter
	// BaseURL is where the app lives, for the checkout return URLs (022b).
	BaseURL string
	// Notifier reaches somebody who is not looking at the app (006p). Nil sends
	// nothing, which is what a deployment with no mail configured does.
	Notifier Notifier
	// SSEInterval is how often the event stream re-reads a job's state. Zero
	// selects a sensible default.
	SSEInterval time.Duration
	// SPA, when set, serves the frontend for any GET path not matched by an API
	// route; it is the public app shell (006e). It is nil in API-only tests.
	SPA http.Handler
}

// Router wires the routes, mounts the gate on the operator set, and keeps the
// gated and open sets enumerable so the two cannot drift. Both come from the
// registration helpers, never from a direct mux.Handle call.
type Router struct {
	mux   *http.ServeMux
	gate  Gate
	gated []Route
	open  []Route

	jobs        JobStore
	projects    ProjectStore
	members     MembershipStore
	profiles    ProfileStore
	repro       ReproReader
	repoPub     RepoPublisher
	repos       RepoProvisioner
	canceler    Canceler
	live        LiveTranscript
	resolver    PaperResolver
	notifier    Notifier
	credits     credit.Store
	reserve     credit.Reserve
	payments    pay.Provider
	policy      TopupPolicy
	baseURL     string
	sseInterval time.Duration
	spa         http.Handler
	// site turns an app path into the absolute URL a crawler needs (042a).
	// There is one per router, so a canonical link and a sitemap entry are the
	// same string.
	site seo.Site
	// modelResolver turns a tier into the model a run would get, so the choice
	// on the Start control can be priced (031). Nil lists the tiers unpriced.
	modelResolver credit.ModelResolver
	drafter       Drafter
	// drafting records which projects have a draft in flight on this server
	// (039), so a burst of requests cannot each buy tokens against one balance.
	drafting struct {
		mu       sync.Mutex
		inFlight map[string]bool
	}
	models modelCache
	// bg tracks work scheduled to outlive the request that started it, which is
	// currently title enrichment. Nothing in the serving path waits on it; tests
	// do.
	bg sync.WaitGroup
	// anonRegistry and sitemapBody hold the two responses served without
	// signing in between rebuilds; see memo. A publication change drops both at
	// once.
	anonRegistry memo[[]ProjectView]
	sitemapBody  memo[[]byte]
}

// defaultSSEInterval is how often the event stream re-reads a job when the
// caller does not choose.
const defaultSSEInterval = time.Second

// NewRouter builds a fully wired router. A nil gate is replaced by DenyAll, which
// is fail-closed.
func NewRouter(d Deps) *Router {
	gate := d.Gate
	if gate == nil {
		gate = DenyAll
	}
	interval := d.SSEInterval
	if interval <= 0 {
		interval = defaultSSEInterval
	}
	rt := &Router{mux: http.NewServeMux(), gate: gate, jobs: d.Jobs, projects: d.Projects, members: d.Members, profiles: d.Profiles, repro: d.Reproductions, repoPub: d.RepoPublisher, repos: d.Repos, canceler: d.Canceler, live: d.Live, resolver: d.Resolver, notifier: d.Notifier, credits: d.Credits, reserve: d.Reserve, payments: d.Payments, policy: d.Platform, modelResolver: d.Models, drafter: d.Drafter, baseURL: d.BaseURL, sseInterval: interval, spa: d.SPA, site: seo.NewSite(d.BaseURL)}
	rt.anonRegistry.ttl, rt.sitemapBody.ttl = publicTTL, publicTTL
	rt.routes()
	return rt
}

// publicChanged is called after any write that changes the public zone: a
// project made public or private, trashed or restored. The held anonymous
// responses are dropped so the next request rebuilds them.
func (rt *Router) publicChanged() {
	rt.anonRegistry.invalidate()
	rt.sitemapBody.invalidate()
}

// Handler returns the wired handler, gate mounted.
func (rt *Router) Handler() http.Handler { return rt.mux }

// GatedRoutes enumerates every operator route from the router itself, so 006c's
// test can assert each answers not_granted for an ungranted principal and that
// no route was registered outside the gate.
func (rt *Router) GatedRoutes() []Route { return append([]Route(nil), rt.gated...) }

// OpenRoutes enumerates the routes deliberately outside the gate.
func (rt *Router) OpenRoutes() []Route { return append([]Route(nil), rt.open...) }

// gatedHandle registers an operator route: it is wrapped in the gate and
// recorded in the gated set. This is the only way a gated route is added, so a
// route cannot be registered open by mistake.
func (rt *Router) gatedHandle(method, pattern string, h http.HandlerFunc) {
	rt.gated = append(rt.gated, Route{method, pattern})
	rt.mux.HandleFunc(method+" "+pattern, rt.guard(limitBody(method, pattern, h)))
}

// openHandle registers a route outside the gate, such as sign-in, /me, or the
// not-invited path, and records it so the two sets are comparable.
func (rt *Router) openHandle(method, pattern string, h http.HandlerFunc) {
	rt.open = append(rt.open, Route{method, pattern})
	rt.mux.HandleFunc(method+" "+pattern, limitBody(method, pattern, h))
}

// DefaultMaxBody bounds a request body on every route that does not name its
// own limit. A megabyte is generous for any JSON this API accepts and small
// enough that a burst of oversized posts cannot fill the process.
const DefaultMaxBody int64 = 1 << 20

// bodyLimits lists the routes whose bodies are bounded by something other than
// the default: the imported record carries a session's evidence, and a
// repository request names one paper key. A route absent here gets
// DefaultMaxBody, so a route registered without a limit is still bounded.
var bodyLimits = map[string]int64{
	"POST /projects/{id}/sessions/import": MaxImportBytes,
	"POST /projects/{id}/repository":      MaxProvisionBytes,
}

// BodyLimit reports the bound a route's body is held to. It serves the test
// that walks every route and any handler that explains a refusal.
func BodyLimit(method, pattern string) int64 {
	if limit, ok := bodyLimits[method+" "+pattern]; ok {
		return limit
	}
	return DefaultMaxBody
}

// limitBody bounds the body of every registered route. A declared length over
// the limit is refused before the handler runs; a body that arrives longer
// than declared, or chunked, is cut off by MaxBytesReader, which makes the
// handler's read fail with an error tooLarge recognises.
func limitBody(method, pattern string, h http.HandlerFunc) http.HandlerFunc {
	limit := BodyLimit(method, pattern)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		h(w, r)
	}
}

// tooLarge reports whether a read failed because the body passed its bound,
// so a handler answers 413 rather than a generic bad request.
func tooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// ReasonUnavailable is the reason a Gate gives when it could not decide: the
// access store did not answer. The guard reports it as 503 rather than 403,
// because a person who is granted must not be told they are not.
const ReasonUnavailable = "access_unavailable"

// guard is the gate middleware: it refuses a request the gate does not allow
// with 403 and the gate's reason, before the handler runs. A gate that could
// not decide answers 503.
func (rt *Router) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, reason := rt.gate.Allow(r); !ok {
			status := http.StatusForbidden
			if reason == ReasonUnavailable {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, reason)
			return
		}
		h(w, r)
	}
}

// writeJSON writes v as JSON with a status code.
//
// The body is encoded before the status line goes out, so a value that cannot
// become JSON answers 500 with a machine-readable code. Encoding straight into
// the ResponseWriter would commit the caller's status first and then break off
// mid-body, which reaches the client as a truncated success it cannot
// distinguish from a real one.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		// writeError's own body is a map[string]string, which always encodes, so
		// this cannot recurse.
		slog.Error("httpapi: encoding a response", "type", fmt.Sprintf("%T", v), "error", err)
		writeError(w, http.StatusInternalServerError, "response_encoding_failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is committed now, so a short write cannot be reported to
	// the client.
	_, _ = w.Write(b)
}

// writeError writes a machine-readable error code.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// writeJSONError writes a machine-readable code together with the details. It
// is used where a bare validation failure is not enough for the reader to act
// on and the list of what failed is the useful part, as with 021a's findings.
func writeJSONError(w http.ResponseWriter, status int, code string, detail []string) {
	writeJSON(w, status, map[string]any{"error": code, "problems": detail})
}

// writeBlockedError refuses a publish and lists the sessions that block it.
// The code names the main reason; the list tells the owner what to resolve.
func writeBlockedError(w http.ResponseWriter, status int, code string, blockers []PublishBlocker) {
	if blockers == nil {
		blockers = []PublishBlocker{}
	}
	writeJSON(w, status, map[string]any{"error": code, "blockers": blockers})
}

// WriteError writes a machine-readable error code, for middleware such as 006c's
// tier guard that refuses a request before the handler runs.
func WriteError(w http.ResponseWriter, status int, code string) {
	writeError(w, status, code)
}

// mustJSON marshals v, returning an empty object on a marshal error, which is
// not expected to occur, so a stream write never panics.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

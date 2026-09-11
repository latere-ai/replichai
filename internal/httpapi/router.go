// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package httpapi is the HTTP server that wraps the audit engine: submit a
// paper, watch a job, read a result. It translates HTTP to the job model and
// back and adds no engine logic — every handler depends on an interface (the
// job store, a registry reader, the gate), never on internal/audit, its method,
// or a provider adapter, so the whole surface tests against fakes with no
// database and no network (006a).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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

// TopupPolicy supplies the purchase spread and floor the checkout route applies
// and the wallet surfaces for the top-up modal (022b). Satisfied by
// platform.Store; a narrow read so the router does not take the whole policy
// store to price one purchase.
type TopupPolicy interface {
	Settings(ctx context.Context) (platform.Settings, error)
}

// Drafter is the model that writes a draft, as this package needs it: a
// completion and the id it is priced under. Satisfied by draft.Model, and
// deliberately restated here so the HTTP layer depends on the shape rather than
// on the package that dials a vendor.
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
// mounts it and decides which routes it covers; 006c supplies the policy (who
// passes). The default is fail-closed — an unconfigured server admits nobody.
type Gate interface {
	// Allow reports whether the request may proceed, and a machine-readable
	// reason to return when it may not (e.g. "not_granted").
	Allow(r *http.Request) (ok bool, reason string)
}

// GateFunc adapts a function to a Gate.
type GateFunc func(r *http.Request) (bool, string)

// Allow calls the function.
func (f GateFunc) Allow(r *http.Request) (bool, string) { return f(r) }

// DenyAll is the fail-closed default gate: it admits nobody, so a server wired
// without a real gate cannot accidentally ship its operator routes open.
var DenyAll Gate = GateFunc(func(*http.Request) (bool, string) { return false, "not_granted" })

// JobStore is the slice of the job store the handlers need — enqueue a
// submission, read one back. Depending on this interface rather than a concrete
// store keeps engine construction in the composition root and out of the
// handlers, which is what the no-engine-logic dependency test enforces.
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
	// DeleteByProject removes a project's sessions — what a permanent project
	// delete does before the project row goes (019).
	DeleteByProject(ctx context.Context, projectID string) error
	// Delete removes one session from the app, softly (032). It is what clears a
	// run that began publishing and never finished, which is otherwise a
	// permanent block on its project going public.
	Delete(ctx context.Context, id string) error
}

// Canceler stops a running session. It is the worker (one process, one
// replica): cancelling interrupts the run in flight and marks it cancelled.
type Canceler interface {
	Cancel(ctx context.Context, id string) error
}

// LiveTranscript reads a run's event stream from the backend by its run id — the
// live trajectory of a session still in flight, before it has published a
// committed transcript. A seam so the handlers stay off the provider adapter.
type LiveTranscript interface {
	Events(ctx context.Context, status provider.RunStatus) ([]provider.Event, error)
}

// ProjectStore is the slice of the project store the handlers need — open a
// project, read one back, list an owner's, and the compensating delete when a
// project's first session fails to enqueue (006g).
type ProjectStore interface {
	Create(ctx context.Context, owner, title string) (project.Project, error)
	Get(ctx context.Context, id string) (project.Project, error)
	ListByOwner(ctx context.Context, owner string) ([]project.Project, error)
	ListPublic(ctx context.Context) ([]project.Project, error)
	// The trash (019): move a project out of every list, take it back, and read
	// what is in there.
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
	// SetTitle renames a project — naming it at create and the rename endpoint
	// (006j). The worker also enriches a placeholder title through this.
	SetTitle(ctx context.Context, id, title string) error
	SetDescription(ctx context.Context, id, description string) error
	// The papers a project is about (012). A paper is recorded before anything
	// runs against it and outlives every session that does, so these sit beside
	// the container rather than being derived from the job store.
	AddPaper(ctx context.Context, projectID string, p project.Paper) error
	ListPapers(ctx context.Context, projectID string) ([]project.Paper, error)
	// PapersByProjects is the same read for a whole list, in one call (043): the
	// registry says what each project on it is a reproduction of, and the route
	// that serves it is the one the internet calls without signing in.
	PapersByProjects(ctx context.Context, ids []string) (map[string][]project.Paper, error)
	EnrichPaper(ctx context.Context, projectID, key, title string, rec paper.Record) (bool, error)
	ListAll(ctx context.Context) ([]project.Project, error)
	EditPaper(ctx context.Context, projectID, key string, edit project.PaperEdit) (project.Paper, error)
	RemovePaper(ctx context.Context, projectID, key string) error
	// MovePaper refiles a paper, and the sessions that are runs of it, under
	// another project (035). One call, because the paper and its runs have to
	// arrive together.
	MovePaper(ctx context.Context, fromID, toID, key string) error
}

// ProfileStore is the researcher profile (006n): the identity a sign-off is
// prefilled from. It is only ever the caller's own — every handler passes the
// principal off the session, never one out of a body — so this seam needs no
// authorisation of its own.
type ProfileStore interface {
	Get(ctx context.Context, principal string) (profile.Profile, error)
	Save(ctx context.Context, p profile.Profile) (profile.Profile, error)
	// ByHandle resolves the public page anyone may read (006r).
	ByHandle(ctx context.Context, handle string) (profile.Profile, error)
	// PrincipalHandle is how a sign-off links to its signer, and the
	// precondition the public flip checks. It resolves through the profile as it
	// is now — never a handle snapshotted onto the project, which would be a
	// second, staler copy of the same person.
	PrincipalHandle(ctx context.Context, principal string) (string, bool, error)
}

// PaperResolver previews a paper reference against the source: it resolves the
// reference to its identity so the New-project screen can show the real title
// before anything is created (006j). A seam so the handlers stay off
// internal/paper; the composition root wires it over paper.Resolve + the
// fetcher. ErrUnresolvable is a bad reference (400); any other error is a source
// that could not be reached (502), kept distinct for the UI.
type PaperResolver interface {
	Resolve(ctx context.Context, ref string) (ResolvedPaper, error)
}

// ResolvedPaper is a reference's previewed identity — the offline key plus the
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

// ErrUnresolvable is what a PaperResolver returns for a reference that is not a
// DOI, arXiv ID, or URL — a 400, distinct from an unreachable source (502).
var ErrUnresolvable = errors.New("unresolvable paper reference")

// MembershipStore is the slice of the project-membership store (006c) the
// members API needs. The owner drives it: the handler authorises on project
// ownership and seeds the owner as the project's lead, so the store's own
// lead-check is satisfied without a second authority model.
type MembershipStore interface {
	List(ctx context.Context, project string) ([]access.Member, error)
	Add(ctx context.Context, byLead access.Principal, project, email string, r access.Role) error
	Remove(ctx context.Context, byLead access.Principal, project, email string) error
	SeedLead(ctx context.Context, project string, lead access.Principal) error
	// Role resolves a caller's role in a project (binding on first login) — the
	// collaborator half of the view predicate.
	Role(ctx context.Context, p access.Principal, project string) (access.Role, bool, error)
	// Projects lists the project ids a principal is a member of — the "shared
	// with me" surface.
	Projects(ctx context.Context, principalSub string) ([]string, error)
}

// Notifier reaches a person who is not in the app (006p). The members route is
// its only caller today: inviting somebody is the one act that asserts
// something about a person who is not present, so it is the one act that has to
// leave the building.
//
// It is an interface rather than *notify.Notifier so httpapi keeps testing
// against a fake, and so a deployment can wire nothing at all.
type Notifier interface {
	ProjectInvitation(ctx context.Context, to, projectID, projectName, inviter string) error
	// AccountFrozen tells a person a clawed-back payment put their account in
	// view-only mode, and that a top-up lifts it (022d).
	AccountFrozen(ctx context.Context, to string) error
}

// RepoPublisher flips a forge repository's visibility — the optional half of the
// project public flip (006f) and of withdrawing it again (006o). It runs with the
// server token, never in the sandbox. Nil when no forge is wired (a local run,
// tests), in which case a project changes visibility in-app without touching a
// repository.
type RepoPublisher interface {
	SetPublic(ctx context.Context, repo string) error
	SetPrivate(ctx context.Context, repo string) error
}

// ReproReader is the read surface over the derived registry (003) and the
// transcript (005). It presents what the forge and the manifests already hold;
// no handler caches a reproduction into its own shape.
//
// A genuine miss must be reported as ErrReproNotFound; any other error is a
// transport or backend failure and becomes a 500, so a canceled request or a
// forge outage is never disguised as "not found".
// It is no longer a user-facing list: the project is the unit of visibility
// (006f), so Get and Transcript here serve internal checks only — the publish
// gate's manifest-clear check and the session transcript reader — never an
// unscoped org-wide listing.
type ReproReader interface {
	Get(ctx context.Context, id string) (Reproduction, error)
	Transcript(ctx context.Context, id string) ([]byte, error)
	// Files lists a repository's committed artifact paths, and Artifact reads one
	// — the in-app file tree (006f), a forge pass-through that works even when the
	// repository itself stays private.
	Files(ctx context.Context, id string) ([]string, error)
	Artifact(ctx context.Context, id, path string) ([]byte, error)
}

// ErrReproNotFound is the sentinel a ReproReader returns for a genuine miss.
var ErrReproNotFound = errors.New("reproduction not found")

// Reproduction is the presented shape of one published reproduction. Field
// details settle here; it is a view over 003's manifest, never a stored copy.
type Reproduction struct {
	ID           string `json:"id"`
	Repo         string `json:"repo"`
	Title        string `json:"title"`
	Verdict      string `json:"verdict"`
	Created      string `json:"created,omitempty"`
	MethodDigest string `json:"method_digest,omitempty"`
	Review       string `json:"review,omitempty"` // 002 redistribution review: clear | excluded
	// CanPublish reports whether the manifest cleared 002's redistribution gate
	// (`review == "clear"`) — the precondition the public flip enforces (006f).
	CanPublish bool `json:"can_publish"`
}

// Deps are what a Router is built from — all interfaces, so tests supply fakes.
type Deps struct {
	Jobs          JobStore
	Projects      ProjectStore
	Members       MembershipStore
	Profiles      ProfileStore
	Reproductions ReproReader
	RepoPublisher RepoPublisher
	// Repos provisions a repository for a session run on somebody else's
	// machine (045l). Nil where the deployment cannot mint a scoped token, in
	// which case the route says so and the terminal keeps its own credential.
	Repos    RepoProvisioner
	Canceler Canceler
	Live     LiveTranscript
	Resolver PaperResolver
	Gate     Gate
	// Credits is the ledger a new project is funded from (011). Nil leaves a
	// project unfunded at creation, which an unmetered offline run wants.
	Credits credit.Store
	// Reserve is how much a new project is funded with when the request names
	// no amount — the same number the enqueue gate will then ask it for.
	Reserve credit.Reserve
	// Payments opens a hosted checkout and verifies its webhook (022b). Nil
	// leaves the checkout route reporting payments_unconfigured — a deployment
	// that does not sell credits still runs.
	Payments pay.Provider
	// Platform supplies the purchase spread and floor. Nil leaves the checkout
	// route and the wallet quote at zero, which an offline run wants.
	Platform TopupPolicy
	// Models resolves a tier to the model a run would get, so the tier list can
	// be priced (031). Satisfied by provider.Provider. Nil lists the tiers with
	// no prices, which is what an offline run with no backend can say.
	Models credit.ModelResolver
	// Drafter writes a project's description when somebody asks for one (039).
	// Nil is a deployment that does not draft: the route answers 503 and the app
	// offers no control, which leaves descriptions typed.
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
	// route — the public app shell (006e). Nil in API-only tests.
	SPA http.Handler
}

// Router wires the routes, mounts the gate on the operator set, and keeps the
// gated and open sets enumerable so the two cannot drift — both come from one
// registration helper, never a second mux.Handle call.
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
	// site turns an app path into the absolute URL a crawler needs (042a). One
	// per router, so a canonical link and a sitemap entry are the same string.
	site seo.Site
	// modelResolver turns a tier into the model a run would get, so the choice
	// on the Start control can be priced (031). Nil lists the tiers unpriced.
	modelResolver credit.ModelResolver
	drafter       Drafter
	// drafting is which projects have a draft in flight here (039), so a burst
	// of requests cannot each buy tokens against one balance.
	drafting struct {
		mu       sync.Mutex
		inFlight map[string]bool
	}
	models modelCache
	// bg tracks work scheduled to outlive the request that started it — title
	// enrichment today. Nothing in the serving path waits on it; tests do.
	bg sync.WaitGroup
}

// defaultSSEInterval is how often the event stream re-reads a job when the
// caller does not choose.
const defaultSSEInterval = time.Second

// NewRouter builds a fully wired router. A nil gate is fail-closed (DenyAll).
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
	rt.routes()
	return rt
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
// recorded in the gated set. This is the only path a gated route is added, so a
// route cannot be registered open by mistake.
func (rt *Router) gatedHandle(method, pattern string, h http.HandlerFunc) {
	rt.gated = append(rt.gated, Route{method, pattern})
	rt.mux.HandleFunc(method+" "+pattern, rt.guard(limitBody(method, pattern, h)))
}

// openHandle registers a route outside the gate (sign-in, /me, the not-invited
// path), recorded so the two sets are comparable.
func (rt *Router) openHandle(method, pattern string, h http.HandlerFunc) {
	rt.open = append(rt.open, Route{method, pattern})
	rt.mux.HandleFunc(method+" "+pattern, limitBody(method, pattern, h))
}

// DefaultMaxBody bounds a request body on every route that does not name its
// own limit. A megabyte is generous for any JSON this API accepts and small
// enough that a burst of oversized posts cannot fill the process.
const DefaultMaxBody int64 = 1 << 20

// bodyLimits are the routes whose bodies are bounded by something other than
// the default: the imported record carries a session's evidence; a repository
// request names one paper key. A route absent here gets DefaultMaxBody, so
// registering a route without thinking about its body still bounds it.
var bodyLimits = map[string]int64{
	"POST /projects/{id}/sessions/import": MaxImportBytes,
	"POST /projects/{id}/repository":      MaxProvisionBytes,
}

// BodyLimit reports the bound a route's body is held to, for the test that
// walks every route and the handler that wants to explain a refusal.
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

// guard is the gate middleware: it refuses a request the gate does not allow
// with 403 and the gate's reason, before the handler runs.
func (rt *Router) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, reason := rt.gate.Allow(r); !ok {
			writeError(w, http.StatusForbidden, reason)
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
		// this cannot come back round.
		log.Printf("httpapi: encoding a %T response: %v", v, err)
		writeError(w, http.StatusInternalServerError, "response_encoding_failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is committed now, so a short write has nowhere left to be
	// reported.
	_, _ = w.Write(b)
}

// writeError writes a machine-readable error code.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// writeJSONError writes a machine-readable code alongside the specifics — used
// where "it did not validate" is not enough for the reader to act on, and the
// list of what failed is the useful part (021a's findings).
func writeJSONError(w http.ResponseWriter, status int, code string, detail []string) {
	writeJSON(w, status, map[string]any{"error": code, "problems": detail})
}

// writeBlockedError refuses a publish and says which sessions stand in the way.
// The code names the headline reason; the list is what the owner is sent to.
func writeBlockedError(w http.ResponseWriter, status int, code string, blockers []PublishBlocker) {
	if blockers == nil {
		blockers = []PublishBlocker{}
	}
	writeJSON(w, status, map[string]any{"error": code, "blockers": blockers})
}

// WriteError writes a machine-readable error code, for middleware (006c's tier
// guard) that refuses a request before the handler runs.
func WriteError(w http.ResponseWriter, status int, code string) {
	writeError(w, status, code)
}

// mustJSON marshals v, returning an empty object on the impossible error so a
// stream write never panics.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

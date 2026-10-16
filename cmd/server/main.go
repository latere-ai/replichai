// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command server is the replichai web application: it wraps the audit engine in
// an HTTP API, runs submissions asynchronously through a Postgres-backed job
// queue, streams their progress, and gates every operator route behind the
// fail-closed access model with an OIDC login. It is the composition root, the
// one place where the engine, the stores, the gate, and the HTTP layer are
// wired together.
//
// Local runs use the in-process "memory" provider and the loopback-only dev
// bypass (`DEV=true`), so `docker compose up` brings the whole system up with no
// IDP and no API key. Production sets `REPLICHAI_RUNTIME=anthropic-managed`, the `AUTH_*` OIDC
// config, and `ADMIN_EMAILS`. Anyone may sign in; budget is what limits them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	// Aliased: "email" is a loop variable in the seeding code below, and a
	// package with the same name would read as a shadowing accident.
	paystripe "latere.ai/x/pay/stripe"
	"latere.ai/x/pkg/authkit/jwt"
	"latere.ai/x/pkg/authkit/oidc"
	xemail "latere.ai/x/pkg/email"
	"latere.ai/x/pkg/hostsandbox"
	xotel "latere.ai/x/pkg/otel"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/notify"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/registry"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
	"github.com/latere-ai/replichai/internal/signin"
	"github.com/latere-ai/replichai/internal/spa"
	"github.com/latere-ai/replichai/internal/webauth"
)

// The dev bypass authenticates as this synthetic principal. It confers nothing
// on its own; ADMIN_EMAILS must include devEmail for it to operate locally.
const (
	devSub   = "dev-user"
	devEmail = "dev@local"

	// serviceName labels every span, metric, and log record this binary emits,
	// and is the name the trace backend groups them under.
	serviceName = "replichai"
)

// version is the build's version string, set by the linker with
// -X main.version=... and attached to every span, metric, and log record. It
// is empty under `go run` and under `go test`, where xotel.Version falls back to
// the module's build info.
var version string

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres URL on the direct endpoint; empty uses in-memory stores, and migrations always use this one")
	dbPoolURL := flag.String("database-pool-url", os.Getenv("DATABASE_POOL_URL"), "Postgres URL on the pooler's endpoint, which serving traffic opens; empty serves on --database-url")
	runtimeName := flag.String("runtime", envOr("REPLICHAI_RUNTIME", "memory"), "managed runtime")
	methodDir := flag.String("method", os.Getenv("METHOD_DIR"), "method directory; empty discovers ./method")
	reproDir := flag.String("repro-dir", os.Getenv("REPRO_DIR"), "filesystem registry source; empty is an empty registry")
	workers := flag.Int("workers", 2, "number of job workers")
	dev := flag.Bool("dev", envBool("DEV"), "enable the loopback-only dev bypass (no OIDC); local use only")
	runnerDelay := flag.String("runner-delay", os.Getenv("RUNNER_DELAY"), "dev: pause per phase so SSE progression is watchable (e.g. 1500ms)")
	flag.Parse()
	baseURL := strings.TrimRight(envOr("BASE_URL", "http://localhost:8080"), "/")
	delay, _ := time.ParseDuration(*runnerDelay)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Telemetry comes first, so every line below is already structured and
	// correlated. The shared library reads the endpoint, headers, and TLS from
	// the standard OTEL_* environment and is a noop when none is set, which is
	// what makes a local run and a test identical to production here.
	logger, shutdownTelemetry, err := xotel.Bootstrap(ctx, xotel.Config{
		ServiceName: serviceName,
		Version:     xotel.Version(version),
	})
	if err != nil {
		// The logger is usable whatever happened; only the OTLP log bridge is
		// in doubt, so this is reported rather than fatal.
		logger.Warn("telemetry: the OTLP log exporter did not start; logs stay local", "error", err)
	}
	defer func() {
		// A fresh context: ctx is cancelled by the time this runs, and a child
		// of it would give the final flush no time at all.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Error("telemetry: flush on shutdown", "error", err)
		}
	}()

	if *dev {
		if err := webauth.CheckDevBypass(*addr, envBool("DEV_INSECURE")); err != nil {
			return err
		}
		logger.Warn("dev bypass enabled: every request is the synthetic principal; local use only", "principal", devEmail)
	}

	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}
	baseCatalog, err := builtin.Catalog(builtin.Config{})
	if err != nil {
		return err
	}
	selected, err := baseCatalog.Canonical(*runtimeName)
	if err != nil {
		return err
	}
	credentials := runtimes.ResolveCredentials(os.LookupEnv)
	catalog, err := builtin.Catalog(builtin.Config{Local: serverLocalConfig(), Anthropic: builtin.AnthropicConfig{
		APIKey: credentials.AnthropicKey, BaseURL: credentials.AnthropicBase,
	}, Latere: builtin.LatereConfig{BaseURL: credentials.LatereAPIURL, TokenCommand: devLatereCommand(*dev, credentials.LatereTokenCommand)}})
	if err != nil {
		return err
	}
	// The runtimes a person may start a session on (048): REPLICHAI_RUNTIMES,
	// or the selected runtime alone. A session naming none runs on the
	// selected one.
	offered, err := offeredRuntimes(baseCatalog, os.Getenv("REPLICHAI_RUNTIMES"), selected)
	if err != nil {
		return err
	}
	selectedRuntime, err := catalog.Build(ctx, string(selected))
	if err != nil {
		return err
	}
	installed := []managed.Runtime{selectedRuntime}
	if selected != "memory" {
		memoryRuntime, buildErr := catalog.Build(ctx, "memory")
		if buildErr != nil {
			return buildErr
		}
		installed = append(installed, memoryRuntime)
	}
	if selected != builtin.AnthropicManaged && credentials.AnthropicKey != "" {
		anthropicRuntime, buildErr := catalog.Build(ctx, string(builtin.AnthropicManaged))
		if buildErr != nil {
			return buildErr
		}
		installed = append(installed, anthropicRuntime)
	}
	built := map[managed.RuntimeName]managed.Runtime{}
	for _, runtime := range installed {
		built[runtime.Name()] = runtime
	}
	for _, name := range offered {
		if _, ok := built[name]; ok {
			continue
		}
		runtime, buildErr := catalog.Build(ctx, string(name))
		if buildErr != nil {
			return buildErr
		}
		built[name] = runtime
		installed = append(installed, runtime)
	}
	runtimeRouter, err := runtimes.NewRouter(selected, installed...)
	if err != nil {
		return err
	}
	controller := managed.NewController(runtimeRouter)
	modelResolver := runtimes.ModelResolver{Runtime: selectedRuntime}
	logger.Info("managed runtime selected", "runtime", selectedRuntime.Name())

	st, err := openStores(ctx, servingURL(*dbPoolURL, *dbURL), *dbURL)
	if err != nil {
		return err
	}
	defer st.close()
	jobStore, projectStore, accessStore, memberStore := st.jobs, st.projects, st.access, st.members
	// A runtime the person pays for acts as the person, so the server keeps
	// their Latere sign-in (048). It is required exactly when one is offered.
	signIns, minter, err := personSignIns(st, offered, devLatereCommand(*dev, credentials.LatereTokenCommand) != "")
	if err != nil {
		return err
	}
	offers := runtimeOffers(offered, selected, built, minter)
	if url := priceListURL(os.Getenv); url != "" {
		if syncer, ok := st.platform.(platform.RateSyncer); ok {
			startPriceSync(ctx, syncer, url)
		}
	}
	reportRateCoverage(ctx, modelResolver, st.platform)

	for _, email := range splitList(os.Getenv("ADMIN_EMAILS")) {
		if err := accessStore.SeedGrant(ctx, email, access.TierAdmin); err != nil {
			return err
		}
		logger.Info("seeded platform admin", "email", email)
	}

	// The forge (006i): with a token, reproductions are created under the org and
	// mounted into runs so the agent commits live; the same client reads them back
	// (003) and makes them public (006f). Without a token this stays nil and the
	// server runs audits without a repository, in transcript-only mode.
	forgeOwner := envOr("REPLICHAI_GITHUB_OWNER", "open-hai")
	forgeToken := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	forgeClient, err := newForgeClient(forgeOwner, forgeToken)
	if err != nil {
		return err
	}
	var runnerForge forge // a nil interface when there is no forge, so the runner runs transcript-only
	var repoProvisioner httpapi.RepoProvisioner
	if forgeClient != nil {
		runnerForge = githubForge{forgeClient}
		// Nil where the credential is a PAT: it authenticates but cannot mint a
		// sub-token of itself, and the route says so rather than handing the
		// organization's own token back over HTTP.
		if forgeClient.CanMint() {
			repoProvisioner = forgeProvisioner{forge: githubForge{forgeClient}, owner: forgeOwner}
		}
	}

	runner, err := newEngineRunner(engineDeps{
		controller: controller,
		sessions:   runtimeRouter,
		method:     m,
		fetch:      newHTTPFetcher(30 * time.Second),
		projects:   projectStore,
		jobs:       jobStore,
		workDir:    filepath.Join(os.TempDir(), "replichai-runs"),
		gh:         runnerForge,
		owner:      forgeOwner,
		token:      forgeToken,
		delay:      delay,
		router:     runtimeRouter,
		signIn:     signInOf(minter),
	})
	if err != nil {
		return err
	}
	worker := job.NewWorker(jobStore, runner, job.WithBudget(spendBackstop(st)))
	if resumed, failed, err := worker.Reconcile(ctx); err != nil {
		logger.Error("reconcile on startup", "error", err)
	} else if resumed > 0 || failed > 0 {
		logger.Info("reconcile", "rejoined", resumed, "failed_unrecoverable", failed)
	}
	go worker.Start(ctx, *workers)

	spaHandler, err := spa.Handler()
	if err != nil {
		return err
	}

	// Reproductions are read from the forge, which is the derived registry
	// (003): the published repositories under the organization are the source
	// of truth. A local run with no forge token falls back to a filesystem
	// directory.
	var reproSource registry.Source
	var repoPublisher httpapi.RepoPublisher
	if forgeClient != nil {
		reproSource = registry.NewGitHubSource(forgeClient, forgeOwner)
		repoPublisher = &forgePublisher{client: forgeClient, owner: forgeOwner}
		logger.Info("reproductions: reading the forge org on GitHub", "owner", forgeOwner)
	} else {
		reproSource = registry.NewFSSource(*reproDir)
		logger.Info("reproductions: reading a filesystem dir; set REPLICHAI_GITHUB_TOKEN to read the forge", "dir", *reproDir)
	}

	notifier, err := buildNotifier(baseURL)
	if err != nil {
		return err
	}

	// The payment processor reads its keys from the environment; with none it is
	// a refusing adapter, so the checkout route reports payments_unconfigured.
	// Sessions are still metered; credits then come only from an admin (022b).
	payments := paystripe.New(paystripe.Config{
		SecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		WebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
	})

	// The drafting model an admin configured, with the deployment's own key
	// (039). It is resolved per request rather than at boot: an admin who
	// changes the model must not have to wait for a deploy, and a deployment
	// with no key configured never offers a draft.
	draftRuntimeKey := ""
	if selected == builtin.AnthropicManaged {
		draftRuntimeKey = credentials.AnthropicKey
	}
	drafter := draftingModel(ctx, st.platform, os.Getenv("DRAFT_API_KEY"), draftRuntimeKey)

	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:          jobStore,
		Projects:      projectStore,
		Members:       memberStore,
		Profiles:      st.profiles,
		Reproductions: &registryReader{source: reproSource},
		RepoPublisher: repoPublisher,
		Repos:         repoProvisioner,
		Canceler:      worker,
		Live:          liveTranscript{router: runtimeRouter},
		Resolver:      paperResolver{fetch: newHTTPFetcher(30 * time.Second)},
		Gate:          webauth.Gate(accessStore),
		Credits:       st.credits,
		Reserve:       platform.StartReserve(st.platform),
		Payments:      payments,
		Platform:      st.platform,
		Models:        modelResolver,
		Runtimes:      offers,
		Drafter:       drafter,
		BaseURL:       baseURL,
		Notifier:      notifier,
		SPA:           spaHandler,
	})

	handler, err := buildHandler(ctx, router.Handler(), accessStore, st.members, st.platform, st.credits, st.projects, sessionCredits(st), router.SweepPapers, *dev, baseURL, signIns)
	if err != nil {
		return err
	}

	srv := newServer(*addr, handler)
	logger.Info("listening", "addr", *addr, "base_url", baseURL)
	return xotel.RunServer(ctx, srv, 10*time.Second, nil)
}

// newServer assembles the listening server around the fully wired surface. The
// tracing middleware sits outermost, so a span exists before the auth
// middleware, the freeze gate, or the mux runs, and every inbound request
// either continues the caller's trace or starts one the outbound calls join.
// The header deadline is separate from any handler timeout: it bounds a client
// that opens a connection and then says nothing.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           xotel.Handler(handler, serviceName),
		ReadHeaderTimeout: 10 * time.Second,
		// An idle kept-alive connection is closed. There is no ReadTimeout and
		// no WriteTimeout by design: the event stream is a response that stays
		// open for as long as a session runs, and either deadline would cancel
		// it mid-run. Request bodies are bounded by the router instead, per
		// route, which is where the size limit belongs.
		IdleTimeout: 2 * time.Minute,
	}
}

// buildHandler assembles the full HTTP surface: the gated operator router at /,
// the open /me, the admin access endpoints behind the tier guard, and either the
// OIDC login routes in production or the dev bypass locally. The auth
// middleware wraps everything and resolves a session, or the dev principal,
// onto each request.
func buildHandler(ctx context.Context, operator http.Handler, acc accessStore, members access.Membership, plat platform.Store, credits credit.Store, projectCaps webauth.ProjectCaps, me webauth.CreditsFunc, sweep webauth.PaperSweep, dev bool, baseURL string, signIns signin.Store) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle("/", operator)
	// /me reports whether the account is frozen (022d), over the same predicate
	// the gate enforces. Nil when there is no ledger, so an offline run reports no
	// frozen field and never dereferences a missing store.
	var frozenFn webauth.FrozenFunc
	if credits != nil {
		frozenFn = func(ctx context.Context, email string) (bool, error) {
			return webauth.Frozen(ctx, credits, acc, email)
		}
	}
	mux.HandleFunc("GET /me", webauth.Me(acc, me, frozenFn))

	// Every admin route is mounted from one place and enumerated, so a route
	// added without the tier guard fails 006k AC1 rather than shipping open.
	webauth.MountAdmin(mux, webauth.RequireTier(acc, access.TierAdmin), webauth.NewAdmin(acc, plat, credits, webauth.WithProjectCaps(projectCaps), webauth.WithOverrides(acc), webauth.WithPaperSweep(sweep)))

	// The freeze gate sits inside the auth middleware, where identity is already
	// on the request, and outside the mux, so every mutating request from a
	// negative-wallet account is refused before any handler runs (022d). Adding
	// /login and /logout to mux below still works: the gate holds the mux by
	// reference and dispatches at request time.
	guarded := webauth.FreezeGate(credits, acc)(mux)

	if dev {
		// The dev bypass has no login step, so the membership binding that
		// sign-in performs runs once at startup for the synthetic principal
		// instead.
		if members != nil {
			if _, err := members.BindAll(ctx, access.Principal{Sub: devSub, Email: devEmail}); err != nil {
				slog.ErrorContext(ctx, "dev: binding memberships", "error", err)
			}
		}
		// There is no OIDC locally: /login returns to the app, already
		// authenticated.
		mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, baseURL+"/", http.StatusFound)
		})
		mux.HandleFunc("GET /logout", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, baseURL+"/", http.StatusFound)
		})
		// The dev bypass refuses to run off loopback, and adding a second
		// credential path must not add a second way past that: the bearer
		// middleware sits inside it, not beside it.
		h, err := bearer(guarded)
		if err != nil {
			return nil, err
		}
		return webauth.DevMiddleware(devSub, devEmail)(h), nil
	}

	login, err := buildLogin(ctx, acc, baseURL, signIns != nil)
	if err != nil {
		return nil, err
	}
	login.BindMemberships(members)
	if signIns != nil {
		login.KeepSignIns(signIns.Put)
	}
	mux.HandleFunc("GET /login", login.HandleLogin)
	mux.HandleFunc("GET /callback", login.HandleCallback)
	mux.HandleFunc("GET /logout", login.HandleLogout)
	// One principal, two credential sources. The bearer runs first and, where
	// one is presented, the cookie is not consulted at all (045j).
	return bearer(login.Middleware(guarded))
}

// serverLocalConfig returns the configuration the local runtime needs when an
// operator selects it for the server. The catalog registers the name whether
// or not it is configured, so without this a REPLICHAI_RUNTIME=local server
// would refuse with a message about composition roots rather than about the
// machine.
func serverLocalConfig() builtin.LocalConfig {
	home, _ := os.UserHomeDir()
	root := os.Getenv("REPLICHAI_SESSION_ROOT")
	if root == "" {
		root = filepath.Join(home, ".replichai", "sessions")
	}
	return builtin.LocalConfig{
		Sandbox:    hostsandbox.DriverName(os.Getenv("REPLICHAI_SANDBOX")),
		Harness:    os.Getenv("REPLICHAI_HARNESS"),
		Engine:     os.Getenv("REPLICHAI_CONTAINER_ENGINE"),
		Image:      os.Getenv("REPLICHAI_HARNESS_IMAGE"),
		ModelsFile: os.Getenv("REPLICHAI_MODELS_FILE"),
		Retention:  os.Getenv("REPLICHAI_SESSION_RETENTION"),
		Root:       root, Home: home,
		Lookup: os.LookupEnv,
	}
}

// bearer is the credential path for the terminal client. A deployment that
// configures no issuer refuses every bearer rather than accepting an unverified
// one, and serves the cookie path unchanged.
func bearer(next http.Handler) (http.Handler, error) {
	issuer := os.Getenv("AUTH_ISSUER")
	jwks := os.Getenv("AUTH_JWKS_URL")
	if issuer != "" && jwks == "" {
		jwks = strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	}
	if issuer == "" || jwks == "" {
		return webauth.Bearer(nil)(next), nil
	}
	v, err := bearerValidator(issuer, jwks, os.Getenv("AUTH_AUDIENCE"))
	if err != nil {
		return nil, err
	}
	return webauth.Bearer(v)(next), nil
}

// bearerValidator is the verifier the bearer path runs: the issuer, its key
// set, and the one audience this deployment accepts. An empty audience
// would admit every token the issuer ever minted, so it is refused.
func bearerValidator(issuer, jwks, audience string) (*jwt.Validator, error) {
	if audience == "" {
		return nil, errors.New("AUTH_ISSUER is set and AUTH_AUDIENCE is empty; name the one audience this server accepts (the deployment sets replichai.latere.ai)")
	}
	return webauth.NewValidator(issuer, jwks, []string{audience}), nil
}

// buildLogin constructs the OIDC login from the AUTH_* environment. A missing
// cookie key or issuer is a startup error, because production must not run the
// operator surface with a broken sign-in.
func buildLogin(ctx context.Context, acc access.Access, baseURL string, keepSignIns bool) (*webauth.Login, error) {
	cookieKey := os.Getenv("AUTH_COOKIE_KEY")
	codec, err := webauth.NewCodec(cookieKey)
	if err != nil {
		return nil, err
	}
	issuer := envOr("AUTH_ISSUER", "https://auth.latere.ai")
	auth, err := oidc.NewProvider(ctx, oidc.ProviderConfig{
		Issuer:       issuer,
		ClientID:     os.Getenv("AUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("AUTH_CLIENT_SECRET"),
		RedirectURL:  envOr("AUTH_REDIRECT_URL", baseURL+"/callback"),
		Scopes:       loginScopes(keepSignIns),
		Kind:         envOr("AUTH_PROVIDER", "latere"),
	})
	if err != nil {
		return nil, err
	}
	// The email may be available only at /userinfo, since latere auth's ID
	// token carries the sub and not the email, and signing out has to end the
	// issuer's session as well as this one. Both endpoints come from one
	// discovery document.
	var userInfo webauth.UserInfoFunc
	var endSession string
	if eps, err := webauth.Discover(ctx, issuer); err != nil {
		slog.WarnContext(ctx, "oidc discovery failed; email comes from the ID token only and sign-out stays local", "error", err)
	} else {
		userInfo = webauth.HTTPUserInfo(eps.UserInfo)
		endSession = eps.EndSession
		if endSession == "" {
			slog.WarnContext(ctx, "issuer advertises no end_session_endpoint; sign-out will not end the provider's session")
		}
	}
	return webauth.NewLogin(auth, codec, acc, baseURL, secureCookies(baseURL), userInfo).EndSessionAt(endSession), nil
}

// accessStore is the access model plus its bootstrap seeder, satisfied by both
// the in-memory and Postgres implementations.
type accessStore interface {
	access.Access
	access.FreezeOverrides
	SeedGrant(ctx context.Context, email string, tier access.Tier) error
}

// sessionCredits answers what /me reports: this person's own wallet, and the
// reserve a project must be able to commit before a session starts there. Both
// come from the same places the gate reads, so the number shown and the number
// that refuses a submit cannot disagree.
func sessionCredits(st stores) webauth.CreditsFunc {
	return func(ctx context.Context, _, email string) (webauth.Credits, error) {
		wallet, err := st.credits.Balance(ctx, credit.Wallet(email))
		if err != nil {
			return webauth.Credits{}, err
		}
		reserve, err := platform.StartReserve(st.platform)(ctx)
		if err != nil {
			return webauth.Credits{}, err
		}
		return webauth.Credits{Wallet: wallet, StartReserve: reserve}, nil
	}
}

// defaultPriceListURL is OpenRouter's public model list, which states
// Anthropic's list prices for every model, both cache-write lifetimes
// included.
const defaultPriceListURL = "https://openrouter.ai/api/v1/models"

// priceListEvery is how often the card is brought back to the price list.
const priceListEvery = time.Hour

// priceListURL is the model list the card's prices follow: OpenRouter's
// public one unless REPLICHAI_PRICE_LIST_URL names another, and none when it
// is "off", for a deployment that prices every model by hand.
func priceListURL(getenv func(string) string) string {
	switch v := strings.TrimSpace(getenv("REPLICHAI_PRICE_LIST_URL")); v {
	case "":
		return defaultPriceListURL
	case "off":
		return ""
	default:
		return v
	}
}

// startPriceSync applies the price list once, before the boot reports its
// rate coverage and serves, and then keeps applying it in the background. A
// first read that fails is logged, and the card keeps its prices until a
// later one succeeds.
func startPriceSync(ctx context.Context, store platform.RateSyncer, url string) {
	client := &http.Client{Timeout: 30 * time.Second, Transport: xotel.Transport(nil)}
	fetch := func(ctx context.Context) ([]credit.Rates, error) { return credit.FetchPriceList(ctx, client, url) }
	if _, err := platform.ApplyPriceList(ctx, store, fetch); err != nil {
		slog.WarnContext(ctx, "prices: the card keeps its prices until the list can be read", "list", url, "error", err)
	}
	go platform.SyncPrices(ctx, store, fetch, priceListEvery)
}

// reportRateCoverage logs, at every boot, whether the rate card names the
// models this backend will run.
//
// It runs here rather than in a command an operator has to remember: the
// server is the one process that holds both the deployment's rate card and the
// provider credential, so it is the only place the question can be answered
// without assembling the two by hand. A CLI check exists as well, for a
// laptop; this one runs whether or not anybody asks.
//
// It logs and never blocks. A preference resolving to a model the card does
// not name is still priced, at the highest rates the card holds, so a vendor's
// new id is never free compute; it is only priced at another model's rate.
// Refusing to boot because a card is a release behind would turn a billing
// imprecision into an outage.
func reportRateCoverage(ctx context.Context, resolver credit.ModelResolver, plat platform.Store) {
	set, err := plat.Settings(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "rates: reading the platform card", "error", err)
		return
	}
	// A short deadline: this is a boot-time report, and a backend that does
	// not answer must not hold up serving.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cov := set.Rates.Covers(ctx, resolver)
	for _, c := range cov {
		switch {
		case c.Err != "":
			slog.WarnContext(ctx, "rates: preference could not be resolved", "preference", c.Preference, "error", firstLine(c.Err))
		case c.Named:
			slog.InfoContext(ctx, "rates: preference priced at its own rates", "preference", c.Preference, "model", c.Model)
		default:
			slog.WarnContext(ctx, "rates: the card does not name this model, so it is charged the dearest rates on the card; "+
				"add it in Admin, Platform to bill it at its real price",
				"preference", c.Preference, "model", c.Model,
				"input_per_mtok", float64(c.Rates.Input)/float64(credit.Dollar),
				"output_per_mtok", float64(c.Rates.Output)/float64(credit.Dollar))
		}
	}
}

// firstLine keeps a multi-line backend error to one log line.
func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before + " …"
	}
	return s
}

// spendBackstop returns the hidden floor under one project's spend (011).
//
// A session runs to completion once it has started, because killing one
// mid-run wastes what it has already spent, so this is not the meter. The
// meter is the start gate: a project that has gone negative cannot begin
// anything until it is topped up. This is the bound on how negative one
// runaway session can take it.
//
// The projection is project-wide, and that is the important part. Accrued
// spend does not reach the ledger until a session settles, so a watchdog that
// subtracted only the observing session's own cost would let N concurrent runs
// each stay above the floor and jointly exceed it.
func spendBackstop(st stores) job.Budget {
	platformFloor := platform.OverdraftCap(st.platform)
	return func(ctx context.Context, j job.Job) (bool, string, error) {
		if j.ProjectID == "" {
			return false, "", nil
		}
		floor, err := platformFloor(ctx)
		if err != nil {
			return false, "", err
		}
		// A project may set its own floor; zero means the platform's.
		if p, err := st.projects.Get(ctx, j.ProjectID); err == nil && p.CreditCap > 0 {
			floor = p.CreditCap
		}
		// Accrued is read before the balance, and the order matters. The two
		// come from different tables on different connections, so a session
		// settling between the reads lands on one side or the other. Read in
		// this order it is counted in the accrual and again in the balance,
		// which makes the projection pessimistic and fires the floor early.
		// Read in the other order it would be missing from both, the
		// projection would look healthier than the project is, and the floor
		// would fire late, which is the one failure this backstop exists to
		// prevent.
		accrued, err := st.jobs.Accrued(ctx, j.ProjectID)
		if err != nil {
			return false, "", err
		}
		balance, err := st.credits.Balance(ctx, credit.Pot(j.ProjectID))
		if err != nil {
			return false, "", err
		}
		if projected := balance - accrued; projected <= -floor {
			return true, fmt.Sprintf("projected balance %d is at or below the floor of -%d (balance %d, accrued %d)",
				projected, floor, balance, accrued), nil
		}
		return false, "", nil
	}
}

// stores holds every store the server composes, so adding one is a field
// rather than another return value on a six-value signature.
type stores struct {
	jobs     job.Store
	projects project.Store
	access   accessStore
	members  access.Membership
	platform platform.Store
	credits  credit.Store
	profiles profile.Store
	// pool is the serving pool, nil for the in-memory stores, for a store
	// built after the others, such as the kept sign-ins.
	pool  *pgxpool.Pool
	close func()
}

// servingURL is the DSN the serving pool opens: the pooler's endpoint when one
// is configured, the direct endpoint otherwise. The fallback is what keeps a
// deployment whose Secret does not carry the pooled key serving exactly as it
// did before the pooler existed.
func servingURL(pooled, direct string) string {
	if pooled != "" {
		return pooled
	}
	return direct
}

// openStores opens every store over one Postgres pool on serving, or their
// in-memory equivalents, and applies the schema over migration. The direct
// endpoint carries the migrations because the pooled endpoint is a
// transaction-mode pool whose size is a fixed claim budgeted for request
// traffic: a boot-time migration there queues behind serving work and holds
// nothing across the transactions the pooler reassigns a backend between.
//
// The in-memory choice is made on migration, the direct endpoint, so a Secret
// carrying only the pooled key is not a configured database.
func openStores(ctx context.Context, serving, migration string) (stores, error) {
	if migration == "" {
		slog.WarnContext(ctx, "no DATABASE_URL: using in-memory stores, so state is lost on restart")
		acc := access.NewMemStore()
		plat := platform.NewMemStore(acc)
		members := access.NewMemMembership()
		ledger := credit.NewMemStore(acc, members, credit.WithOnSolvent(thawOnSolvent(acc)))
		jobs := job.NewMemStore(job.WithCredits(ledger, platform.StartReserve(plat)), job.WithRates(platform.RateSnapshot(plat)))
		return stores{
			jobs: jobs,
			// The job store goes into the project store so a moved paper takes its
			// sessions with it (035); without it MovePaper refuses.
			projects: project.NewMemStore(project.WithSessions(jobs)),
			access:   acc,
			members:  members,
			platform: plat,
			credits:  ledger,
			profiles: profile.NewMemStore(),
			close:    func() {},
		}, nil
	}
	pool, err := pgxpool.New(ctx, serving)
	if err != nil {
		return stores{}, fmt.Errorf("open serving pool: %w", err)
	}
	if err := migrateAll(ctx, migration); err != nil {
		pool.Close()
		return stores{}, err
	}
	slog.InfoContext(ctx, "connected to Postgres and applied migrations")
	acc := access.NewPGAccess(pool)
	plat := platform.NewPGStore(pool, acc)
	members := access.NewPGMembership(pool)
	ledger := credit.NewPGStore(pool, acc, members, credit.WithOnSolvent(thawOnSolvent(acc)))
	jobs := job.NewPGStore(pool, job.WithPGCredits(ledger, platform.StartReserve(plat)), job.WithPGRates(platform.RateSnapshot(plat)))
	return stores{
		jobs:     jobs,
		projects: project.NewPGStore(pool, project.WithPGSessions(jobs)),
		access:   acc,
		members:  members,
		platform: plat,
		credits:  ledger,
		profiles: profile.NewPGStore(pool),
		pool:     pool,
		close:    pool.Close,
	}, nil
}

// migrationPoolConfig parses migrationURL and bounds what a migration holds on
// the direct endpoint: one connection, none of them idle.
func migrationPoolConfig(migrationURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(migrationURL)
	if err != nil {
		return nil, fmt.Errorf("open migration pool: %w", err)
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	return cfg, nil
}

// migrateAll applies every namespace's schema over migrationURL and closes the
// connection it used. The project migration runs after the job migration
// because its backfill reads the jobs table.
//
// The pool is capped at one connection because one is all a migration uses,
// and the whole pool is released before the server serves a request: a
// migration is a boot-time claim on the direct endpoint, not a standing one.
func migrateAll(ctx context.Context, migrationURL string) error {
	cfg, err := migrationPoolConfig(migrationURL)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open migration pool: %w", err)
	}
	defer pool.Close()
	if err := waitForDB(ctx, pool); err != nil {
		return fmt.Errorf("reach the migration endpoint: %w", err)
	}
	for _, m := range []struct {
		name string
		fn   func(context.Context, *pgxpool.Pool) error
	}{
		{"job", job.Migrate},
		{"access", access.Migrate},
		{"project", project.Migrate}, // after job: its backfill reads jobs
		{"platform", platform.Migrate},
		{"profile", profile.Migrate},
		{"credit", credit.Migrate},
		{"signin", signin.Migrate},
	} {
		if err := m.fn(ctx, pool); err != nil {
			return fmt.Errorf("%s migrate: %w", m.name, err)
		}
	}
	return nil
}

// thawOnSolvent clears an account's freeze override as soon as a top-up leaves
// its wallet solvent (022d). The override is a waiver of a debt; paying the
// debt ends it, and a later debt is a separate decision.
func thawOnSolvent(ov access.FreezeOverrides) func(context.Context, string) {
	return func(ctx context.Context, email string) {
		if err := ov.ClearOverride(ctx, email); err != nil {
			slog.WarnContext(ctx, "freeze override not retired after top-up", "email", email, "error", err)
		}
	}
}

// waitForDB retries until Postgres accepts a connection, so the app can start
// alongside its database under compose without a race.
func waitForDB(ctx context.Context, pool *pgxpool.Pool) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := pool.Ping(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return pool.Ping(ctx)
		}
		time.Sleep(time.Second)
	}
}

// buildNotifier configures outbound mail (006p) from the environment: Mailgun
// when MAILGUN_API_KEY is a real key, SMTP when SMTP_HOST is set, and a
// log-only transport otherwise, so a local run and CI need no credentials and
// no network, and an invitation is written to the log rather than dropped.
//
// A misconfiguration, such as a key with no domain, is a startup error.
// Discovering it at the first invitation instead would mean the one message
// the platform sends to somebody who is not present is the one that fails.
func buildNotifier(baseURL string) (*notify.Notifier, error) {
	tx, err := xemail.New(
		os.Getenv("MAILGUN_API_KEY"),
		os.Getenv("MAILGUN_DOMAIN"),
		os.Getenv("MAILGUN_REGION"),
		&xemail.SMTPConfig{
			Host:     os.Getenv("SMTP_HOST"),
			Port:     os.Getenv("SMTP_PORT"),
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
			From:     envOr("SMTP_FROM", "noreply@latere.ai"),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("mail: %w", err)
	}
	if _, unconfigured := tx.(*xemail.LogSender); unconfigured {
		slog.Warn("mail: no transport configured; invitations will be logged, not sent")
	}
	return notify.New(tx, baseURL), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newForgeClient builds the GitHub client the server publishes and reads with.
// It prefers a GitHub App, configured by REPLICHAI_GITHUB_APP_ID and
// REPLICHAI_GITHUB_APP_KEY, because only an App can mint the scoped, revocable
// mount token (009); the key value is the PEM text itself or a path to a .pem
// file. It falls back to the personal access token in REPLICHAI_GITHUB_TOKEN,
// and to nil when neither is set, which is transcript-only mode. An App that is
// configured but invalid is a fatal misconfiguration rather than a fallback to
// the PAT, so a broken App is noticed at boot.
func newForgeClient(owner, pat string) (*github.Client, error) {
	appID := strings.TrimSpace(os.Getenv("REPLICHAI_GITHUB_APP_ID"))
	appKey := os.Getenv("REPLICHAI_GITHUB_APP_KEY")
	if appID != "" && appKey != "" {
		id, err := strconv.ParseInt(appID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("REPLICHAI_GITHUB_APP_ID %q is not a number: %w", appID, err)
		}
		key, err := loadAppKey(appKey)
		if err != nil {
			return nil, err
		}
		c, err := github.NewApp(id, key, owner)
		if err != nil {
			return nil, err
		}
		slog.Info("forge: authenticating as a GitHub App", "app_id", id, "owner", owner)
		return c, nil
	}
	if pat != "" {
		slog.Info("forge: authenticating with a personal access token; set REPLICHAI_GITHUB_APP_ID and REPLICHAI_GITHUB_APP_KEY to use the App")
		return github.New(pat), nil
	}
	return nil, nil
}

// loadAppKey returns the App private key bytes from the env value, which is
// either the PEM text itself or a path to a .pem file.
func loadAppKey(v string) ([]byte, error) {
	if strings.Contains(v, "-----BEGIN") {
		return []byte(v), nil
	}
	// G703: v is REPLICHAI_GITHUB_APP_KEY from the process environment, read at
	// boot by its one caller, newForgeClient. It is deployment configuration
	// rather than request input, and naming a file is what the variable is for.
	b, err := os.ReadFile(strings.TrimSpace(v)) //nolint:gosec // G703: the only caller passes REPLICHAI_GITHUB_APP_KEY, boot-time deployment config that is meant to name a .pem path.
	if err != nil {
		return nil, fmt.Errorf("reading REPLICHAI_GITHUB_APP_KEY file %q: %w", v, err)
	}
	return b, nil
}

// secureCookies reports whether the session cookie may carry Secure and the
// __Host- prefix. Only the scheme of the base URL decides it. A plain-HTTP
// local run cannot use either, because the browser rejects __Host- without
// Secure and a Secure cookie is never sent back over http, and an https
// deployment always can.
//
// There is no override by design. One existed, AUTH_INSECURE_COOKIES, and it
// could only take effect on an https deployment: on plain HTTP the scheme has
// already answered false, so the variable changed nothing there. Its only
// reachable effect was to strip Secure and __Host- from a deployment serving
// real traffic over TLS, which no developer needs and which an attacker
// wants. Nothing set it: not compose, not an overlay, not example.env. Unlike
// DEV_INSECURE, which gives a containerized local run something it needs and
// is refused off loopback, this variable had no use.
func secureCookies(baseURL string) bool { return strings.HasPrefix(baseURL, "https") }

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "yes"
}

// draftingModel builds the Drafter over whatever the admin configured (039).
// It returns nil when no credential is present. A deployment with no model
// configured still gets a Drafter, whose Name is empty, which the route
// refuses as this deployment not offering drafts.
//
// The settings are re-read per call rather than read at boot: the model is an
// admin's setting, and a setting that needed a deploy to take effect would be
// no better than a constant. The credential is not re-read: it is the
// deployment's, read once from the environment, and it never reaches the
// settings row.
func draftingModel(ctx context.Context, plat platform.Store, draftKey, agentKey string) httpapi.Drafter {
	if strings.TrimSpace(draftKey) == "" && strings.TrimSpace(agentKey) == "" {
		slog.InfoContext(ctx, "drafting: no credential; project descriptions are typed")
		return nil
	}
	if set, err := plat.Settings(ctx); err == nil && !set.Draft.Configured() {
		slog.InfoContext(ctx, "drafting: no model configured; project descriptions are typed")
	}
	return &configuredDrafter{plat: plat, draftKey: draftKey, agentKey: agentKey}
}

// configuredDrafter resolves the configured model at the moment it is asked.
type configuredDrafter struct {
	plat platform.Store
	// draftKey is the credential for a drafting endpoint the deployment would
	// not otherwise hold; agentKey is the one it already holds for the agent
	// that runs its sessions. Choosing Anthropic uses the second, so pointing
	// drafting at the vendor already in use costs an operator one model id and
	// nothing else.
	draftKey, agentKey string
}

// keyFor picks the credential a provider needs. Anthropic falls back to the
// agent's key; anything else is a vendor this deployment has no other reason to
// hold a key for, so it must be given one.
func (d *configuredDrafter) keyFor(c draft.Config) string {
	if strings.EqualFold(strings.TrimSpace(c.Provider), "anthropic") && strings.TrimSpace(d.draftKey) == "" {
		return d.agentKey
	}
	return d.draftKey
}

// Name is the model id the answer will be priced under. An unconfigured
// deployment has none, and the empty string matches no rate-card entry, which
// is the refusal the drafting route already makes.
func (d *configuredDrafter) Name() string {
	set, err := d.plat.Settings(context.Background())
	if err != nil {
		return ""
	}
	return set.Draft.Model
}

func (d *configuredDrafter) Complete(ctx context.Context, r draft.Request) (draft.Result, error) {
	set, err := d.plat.Settings(ctx)
	if err != nil {
		return draft.Result{}, fmt.Errorf("reading the drafting settings: %w", err)
	}
	m, err := draft.New(set.Draft, d.keyFor(set.Draft), nil)
	if err != nil {
		return draft.Result{}, err
	}
	return m.Complete(ctx, r)
}

// splitList splits a comma- or space-separated list, dropping blanks.
func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

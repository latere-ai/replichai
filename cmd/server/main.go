// Command server is the replichai web application: it wraps the audit engine in
// an HTTP API, runs submissions asynchronously through a Postgres-backed job
// queue, streams their progress, and gates every operator route behind the
// fail-closed access model with an OIDC login. It is the composition root — the
// one place the engine, the stores, the gate, and the HTTP layer are wired.
//
// Local runs use the in-process "memory" provider and the loopback-only dev
// bypass (`DEV=true`), so `docker compose up` brings the whole system up with no
// IDP and no API key. Production sets `PROVIDER=anthropic`, the `AUTH_*` OIDC
// config, and `ADMIN_EMAILS`, and admits only granted principals.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"latere.ai/x/pkg/oidclogin"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/registry"
	"github.com/latere-ai/replichai/internal/spa"
	"github.com/latere-ai/replichai/internal/webauth"

	// Register the provider adapters so provider.Get can resolve them.
	_ "github.com/latere-ai/replichai/internal/provider/anthropic"
	_ "github.com/latere-ai/replichai/internal/provider/memory"
)

// The dev bypass authenticates as this synthetic principal. It confers nothing
// on its own — ADMIN_EMAILS must include devEmail for it to operate locally.
const (
	devSub   = "dev-user"
	devEmail = "dev@local"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func run() error {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres URL; empty uses in-memory stores")
	providerName := flag.String("provider", envOr("PROVIDER", "memory"), "engine provider: memory | anthropic")
	methodDir := flag.String("method", os.Getenv("METHOD_DIR"), "method directory; empty discovers ./method")
	reproDir := flag.String("repro-dir", os.Getenv("REPRO_DIR"), "filesystem registry source; empty is an empty registry")
	workers := flag.Int("workers", 2, "number of job workers")
	dev := flag.Bool("dev", envBool("DEV"), "enable the loopback-only dev bypass (no OIDC); local use only")
	runnerDelay := flag.String("runner-delay", os.Getenv("RUNNER_DELAY"), "dev: pause per phase so SSE progression is watchable (e.g. 1500ms)")
	flag.Parse()

	baseURL := strings.TrimRight(envOr("BASE_URL", "http://localhost:8080"), "/")
	delay, _ := time.ParseDuration(*runnerDelay)

	if *dev {
		if err := webauth.CheckDevBypass(*addr, envBool("DEV_INSECURE")); err != nil {
			return err
		}
		log.Printf("WARNING: dev bypass enabled — every request is the synthetic principal %q; local use only", devEmail)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}
	prov, err := provider.Get(*providerName)
	if err != nil {
		return err
	}
	log.Printf("provider: %s", prov.Name())

	st, err := openStores(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer st.close()
	jobStore, projectStore, accessStore, memberStore := st.jobs, st.projects, st.access, st.members

	for _, email := range splitList(os.Getenv("ADMIN_EMAILS")) {
		if err := accessStore.SeedGrant(ctx, email, access.TierAdmin); err != nil {
			return err
		}
		log.Printf("seeded platform admin: %s", email)
	}
	for _, email := range splitList(os.Getenv("SEED_EMAILS")) {
		if err := accessStore.SeedGrant(ctx, email, access.TierUser); err != nil {
			return err
		}
		log.Printf("seeded user: %s", email)
	}

	// The forge (006i): with a token, reproductions are created under the org and
	// mounted into runs so the agent commits live; the same client reads them back
	// (003) and flips them public (006f). Without a token this stays nil and the
	// server runs audits without a repository — transcript-only.
	var forgeClient *github.Client
	forgeOwner := envOr("REPLICHAI_GITHUB_OWNER", "open-hai")
	forgeToken := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	var runnerForge forge // nil interface when no token — runner runs transcript-only
	if forgeToken != "" {
		forgeClient = github.New(forgeToken)
		runnerForge = githubForge{forgeClient}
	}

	runner := &engineRunner{
		provider: prov,
		method:   m,
		fetch:    newHTTPFetcher(30 * time.Second),
		projects: projectStore,
		jobs:     jobStore,
		workDir:  filepath.Join(os.TempDir(), "replichai-runs"),
		gh:       runnerForge,
		owner:    forgeOwner,
		token:    forgeToken,
		delay:    delay,
	}
	worker := job.NewWorker(jobStore, runner)
	if resumed, failed, err := worker.Reconcile(ctx); err != nil {
		log.Printf("reconcile on startup: %v", err)
	} else if resumed > 0 || failed > 0 {
		log.Printf("reconcile: rejoined %d live run(s), failed %d unrecoverable", resumed, failed)
	}
	go worker.Start(ctx, *workers)

	spaHandler, err := spa.Handler()
	if err != nil {
		return err
	}

	// Reproductions are read from the forge (the derived registry, 003): the
	// published repositories under the org are the source of truth. Falls back to
	// a filesystem dir for a local run with no forge token.
	var reproSource registry.Source
	var repoPublisher httpapi.RepoPublisher
	if forgeClient != nil {
		reproSource = registry.NewGitHubSource(forgeClient, forgeOwner)
		repoPublisher = &forgePublisher{client: forgeClient, owner: forgeOwner}
		log.Printf("reproductions: reading the %s org on GitHub", forgeOwner)
	} else {
		reproSource = registry.NewFSSource(*reproDir)
		log.Printf("reproductions: reading filesystem dir %q (set REPLICHAI_GITHUB_TOKEN to read the forge)", *reproDir)
	}

	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:          jobStore,
		Projects:      projectStore,
		Members:       memberStore,
		Reproductions: &registryReader{source: reproSource},
		RepoPublisher: repoPublisher,
		Canceler:      worker,
		Live:          liveTranscript{prov: prov},
		Resolver:      paperResolver{fetch: newHTTPFetcher(30 * time.Second)},
		Gate:          webauth.Gate(accessStore),
		SPA:           spaHandler,
	})

	handler, err := buildHandler(ctx, router.Handler(), accessStore, st.platform, sessionQuota(st), *dev, baseURL)
	if err != nil {
		return err
	}

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("listening on %s (base %s)", *addr, baseURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// buildHandler assembles the full HTTP surface: the gated operator router at /,
// the open /me, the admin access endpoints behind the tier guard, and either the
// OIDC login routes (prod) or the dev bypass (local). The auth middleware wraps
// everything, resolving a session (or the dev principal) onto each request.
func buildHandler(ctx context.Context, operator http.Handler, acc accessStore, plat platform.Store, quota webauth.QuotaFunc, dev bool, baseURL string) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle("/", operator)
	mux.HandleFunc("GET /me", webauth.Me(acc, quota))

	// Every admin route is mounted from one place and enumerated, so a route
	// added without the tier guard fails 006k AC1 rather than shipping open.
	webauth.MountAdmin(mux, webauth.RequireTier(acc, access.TierAdmin), webauth.NewAdmin(acc, plat))

	if dev {
		// No OIDC locally: /login just returns to the app, already authenticated.
		mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, baseURL+"/", http.StatusFound)
		})
		mux.HandleFunc("GET /logout", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, baseURL+"/", http.StatusFound)
		})
		return webauth.DevMiddleware(devSub, devEmail)(mux), nil
	}

	login, err := buildLogin(ctx, acc, baseURL)
	if err != nil {
		return nil, err
	}
	mux.HandleFunc("GET /login", login.HandleLogin)
	mux.HandleFunc("GET /callback", login.HandleCallback)
	mux.HandleFunc("GET /logout", login.HandleLogout)
	return login.Middleware(mux), nil
}

// buildLogin constructs the OIDC login from the AUTH_* environment. A missing
// cookie key or issuer is a startup error — production must not run the operator
// surface with a broken sign-in.
func buildLogin(ctx context.Context, acc access.Access, baseURL string) (*webauth.Login, error) {
	cookieKey := os.Getenv("AUTH_COOKIE_KEY")
	codec, err := webauth.NewCodec(cookieKey)
	if err != nil {
		return nil, err
	}
	issuer := envOr("AUTH_ISSUER", "https://auth.latere.ai")
	auth, err := oidclogin.New(ctx, oidclogin.Config{
		Issuer:       issuer,
		ClientID:     os.Getenv("AUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("AUTH_CLIENT_SECRET"),
		RedirectURL:  envOr("AUTH_REDIRECT_URL", baseURL+"/callback"),
		Provider:     envOr("AUTH_PROVIDER", "latere"),
	})
	if err != nil {
		return nil, err
	}
	// The email may live only at /userinfo (latere auth's ID token carries the
	// sub, not the email), so discover it and fetch it in the callback.
	var userInfo webauth.UserInfoFunc
	if endpoint, err := webauth.DiscoverUserInfo(ctx, issuer); err != nil {
		log.Printf("userinfo discovery failed (%v); email will come from the ID token only", err)
	} else {
		userInfo = webauth.HTTPUserInfo(endpoint)
	}
	secure := strings.HasPrefix(baseURL, "https") && !envBool("AUTH_INSECURE_COOKIES")
	return webauth.NewLogin(auth, codec, acc, baseURL, secure, userInfo), nil
}

// accessStore is the access model plus its bootstrap seeder, satisfied by both
// the in-memory and Postgres implementations.
type accessStore interface {
	access.Access
	SeedGrant(ctx context.Context, email string, tier access.Tier) error
	// EmailOf prices a payer known only by their sub against the email-keyed
	// quota top-ups (006l). Widened here rather than on access.Access, which
	// stays the authorisation contract.
	EmailOf(ctx context.Context, principalSub string) (string, bool, error)
}

// sessionQuota answers what /me reports: how much of the payer's allowance is
// spent, and what the allowance is. Both halves read the same predicate the
// enqueue check applies, so the number shown and the number that refuses a
// submit cannot disagree.
func sessionQuota(st stores) webauth.QuotaFunc {
	return func(ctx context.Context, principalSub, email string) (webauth.Quota, error) {
		limit, unlimited, err := st.platform.Allowance(ctx, access.Principal{Sub: principalSub, Email: email})
		if err != nil {
			return webauth.Quota{}, err
		}
		used, err := st.jobs.Consumed(ctx, principalSub)
		if err != nil {
			return webauth.Quota{}, err
		}
		return webauth.Quota{Used: used, Limit: limit, Unlimited: unlimited}, nil
	}
}

// sessionAllowance is how the job store learns what a payer may spend: the
// platform default plus that person's top-up, or unlimited for an admin. It is
// the one adapter between the policy store and the enforcement point, so
// internal/job never imports internal/platform.
//
// A payer whose email cannot be read is priced at the plain default rather than
// refused or waved through — fail-closed, and a top-up they may have is simply
// not applied.
func sessionAllowance(acc accessStore, plat platform.Store) job.Allowance {
	return func(ctx context.Context, payerSub string) (int, bool, error) {
		email, _, err := acc.EmailOf(ctx, payerSub)
		if err != nil {
			log.Printf("quota: reading the payer's email for %s: %v", payerSub, err)
		}
		return plat.Allowance(ctx, access.Principal{Sub: payerSub, Email: email})
	}
}

// stores is every persistence seam the server composes, so adding one is a
// field rather than another return value on a six-value signature.
type stores struct {
	jobs     job.Store
	projects project.Store
	access   accessStore
	members  access.Membership
	platform platform.Store
	close    func()
}

// openStores opens every store over one Postgres pool (or their in-memory
// equivalents), migrating each. The project migration runs after the job
// migration: its backfill reads the jobs table. The platform store is built
// last because it re-checks admin tier through the access store.
func openStores(ctx context.Context, dbURL string) (stores, error) {
	if dbURL == "" {
		log.Printf("no DATABASE_URL: using in-memory stores (state is lost on restart)")
		acc := access.NewMemStore()
		plat := platform.NewMemStore(acc)
		return stores{
			jobs:     job.NewMemStore(job.WithAllowance(sessionAllowance(acc, plat))),
			projects: project.NewMemStore(),
			access:   acc,
			members:  access.NewMemMembership(),
			platform: plat,
			close:    func() {},
		}, nil
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return stores{}, err
	}
	if err := waitForDB(ctx, pool); err != nil {
		pool.Close()
		return stores{}, err
	}
	for _, m := range []struct {
		name string
		fn   func(context.Context, *pgxpool.Pool) error
	}{
		{"job", job.Migrate},
		{"access", access.Migrate},
		{"project", project.Migrate}, // after job: its backfill reads jobs
		{"platform", platform.Migrate},
	} {
		if err := m.fn(ctx, pool); err != nil {
			pool.Close()
			return stores{}, fmt.Errorf("%s migrate: %w", m.name, err)
		}
	}
	log.Printf("connected to Postgres and applied migrations")
	acc := access.NewPGAccess(pool)
	plat := platform.NewPGStore(pool, acc)
	return stores{
		jobs:     job.NewPGStore(pool, job.WithPGAllowance(sessionAllowance(acc, plat))),
		projects: project.NewPGStore(pool),
		access:   acc,
		members:  access.NewPGMembership(pool),
		platform: plat,
		close:    pool.Close,
	}, nil
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "yes"
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

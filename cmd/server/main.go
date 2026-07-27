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

	jobStore, projectStore, accessStore, memberStore, closeStores, err := openStores(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer closeStores()

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

	runner := &engineRunner{
		provider: prov,
		method:   m,
		fetch:    newHTTPFetcher(30 * time.Second),
		projects: projectStore,
		workDir:  filepath.Join(os.TempDir(), "replichai-runs"),
		delay:    delay,
	}
	worker := job.NewWorker(jobStore, runner)
	if n, err := worker.Reap(ctx); err != nil {
		log.Printf("reap on startup: %v", err)
	} else if n > 0 {
		log.Printf("reaped %d jobs stranded by a prior restart", n)
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
	if token := os.Getenv("REPLICHAI_GITHUB_TOKEN"); token != "" {
		owner := envOr("REPLICHAI_GITHUB_OWNER", "open-hai")
		client := github.New(token)
		reproSource = registry.NewGitHubSource(client, owner)
		repoPublisher = &forgePublisher{client: client, owner: owner}
		log.Printf("reproductions: reading the %s org on GitHub", owner)
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
		Gate:          webauth.Gate(accessStore),
		SPA:           spaHandler,
	})

	handler, err := buildHandler(ctx, router.Handler(), accessStore, *dev, baseURL)
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
func buildHandler(ctx context.Context, operator http.Handler, acc accessStore, dev bool, baseURL string) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle("/", operator)
	mux.HandleFunc("GET /me", webauth.Me(acc))

	admin := webauth.NewAdmin(acc)
	requireAdmin := webauth.RequireTier(acc, access.TierAdmin)
	mux.Handle("GET /admin/access", requireAdmin(http.HandlerFunc(admin.List)))
	mux.Handle("POST /admin/access", requireAdmin(http.HandlerFunc(admin.Grant)))
	mux.Handle("DELETE /admin/access/{email}", requireAdmin(http.HandlerFunc(admin.Revoke)))

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
}

// openStores returns the job, project, and access stores over one Postgres pool
// (or their in-memory equivalents), migrating each, with a matching close
// function. The project migration runs after the job migration: its backfill
// reads the jobs table.
func openStores(ctx context.Context, dbURL string) (job.Store, project.Store, accessStore, access.Membership, func(), error) {
	if dbURL == "" {
		log.Printf("no DATABASE_URL: using in-memory stores (state is lost on restart)")
		return job.NewMemStore(), project.NewMemStore(), access.NewMemStore(), access.NewMemMembership(), func() {}, nil
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := waitForDB(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, nil, nil, nil, err
	}
	for _, m := range []struct {
		name string
		fn   func(context.Context, *pgxpool.Pool) error
	}{
		{"job", job.Migrate},
		{"access", access.Migrate},
		{"project", project.Migrate}, // after job: its backfill reads jobs
	} {
		if err := m.fn(ctx, pool); err != nil {
			pool.Close()
			return nil, nil, nil, nil, nil, err
		}
	}
	log.Printf("connected to Postgres and applied migrations")
	return job.NewPGStore(pool), project.NewPGStore(pool), access.NewPGAccess(pool), access.NewPGMembership(pool), pool.Close, nil
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

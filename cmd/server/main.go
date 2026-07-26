// Command server is the replichai web application: it wraps the audit engine in
// an HTTP API, runs submissions asynchronously through a Postgres-backed job
// queue, and streams their progress. It is the composition root — the one place
// the engine, the job store, and the HTTP layer are wired together.
//
// For a self-contained local run it defaults to the in-process "memory"
// provider, so `docker compose up` brings the whole system up with no API key
// and jobs flow submitted -> done in seconds. Point --provider at "anthropic"
// (with REPLICHAI_API_KEY) for real audits.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/registry"

	// Register the provider adapters so provider.Get can resolve them.
	_ "github.com/latere-ai/replichai/internal/provider/anthropic"
	_ "github.com/latere-ai/replichai/internal/provider/memory"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func run() error {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres URL; empty uses an in-memory store")
	providerName := flag.String("provider", envOr("PROVIDER", "memory"), "engine provider: memory | anthropic")
	methodDir := flag.String("method", os.Getenv("METHOD_DIR"), "method directory; empty discovers ./method")
	reproDir := flag.String("repro-dir", os.Getenv("REPRO_DIR"), "filesystem registry source; empty is an empty registry")
	workers := flag.Int("workers", 2, "number of job workers")
	dev := flag.Bool("dev", envBool("DEV"), "enable the INSECURE dev gate (no auth); local use only")
	runnerDelay := flag.String("runner-delay", os.Getenv("RUNNER_DELAY"), "dev: pause per phase so SSE progression is watchable (e.g. 1500ms)")
	flag.Parse()

	delay, _ := time.ParseDuration(*runnerDelay) // empty or bad -> 0, no delay

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

	store, closeStore, err := openStore(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer closeStore()

	runner := &engineRunner{
		provider: prov,
		method:   m,
		fetch:    newHTTPFetcher(30 * time.Second),
		workDir:  filepath.Join(os.TempDir(), "replichai-runs"),
		delay:    delay,
	}
	if delay > 0 {
		log.Printf("runner delay: %s per phase (dev; makes SSE progression watchable)", delay)
	}
	worker := job.NewWorker(store, runner)
	if n, err := worker.Reap(ctx); err != nil {
		log.Printf("reap on startup: %v", err)
	} else if n > 0 {
		log.Printf("reaped %d jobs stranded by a prior restart", n)
	}
	go worker.Start(ctx, *workers)

	gate := httpapi.DenyAll
	var wrap func(http.Handler) http.Handler
	if *dev {
		log.Printf("WARNING: --dev enables an INSECURE gate that admits every request as %q; never expose this", devPrincipal)
		gate = devGate
		wrap = withDevPrincipal
	}

	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:          store,
		Reproductions: &registryReader{source: registry.NewFSSource(*reproDir)},
		Gate:          gate,
	})
	var handler http.Handler = router.Handler()
	if wrap != nil {
		handler = wrap(handler)
	}

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// openStore returns a Postgres store when a URL is set (migrating first), or an
// in-memory store otherwise, with a matching close function.
func openStore(ctx context.Context, dbURL string) (job.Store, func(), error) {
	if dbURL == "" {
		log.Printf("no DATABASE_URL: using the in-memory job store (jobs are lost on restart)")
		return job.NewMemStore(), func() {}, nil
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, nil, err
	}
	if err := waitForDB(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := job.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	log.Printf("connected to Postgres and applied migrations")
	return job.NewPGStore(pool), pool.Close, nil
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

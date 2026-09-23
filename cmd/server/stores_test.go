// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
)

// poolHost and directHost stand in for the two endpoints. They are distinct so
// an error that names one proves which DSN the failing client was handed.
const (
	poolHost   = "pooled.invalid"
	directHost = "directhost.invalid"
)

// TestServingURLPrefersThePool pins which endpoint serving traffic opens. The
// pooled DSN is the claim on the cluster that does not move with the replica
// count, so it wins whenever it is set.
func TestServingURLPrefersThePool(t *testing.T) {
	got := servingURL("postgres://pooled:25061/replichai-pool", "postgres://direct:5432/replichai")
	if want := "postgres://pooled:25061/replichai-pool"; got != want {
		t.Fatalf("servingURL() = %q, want the pooled endpoint %q", got, want)
	}
}

// TestServingURLFallsBackToDirect pins the behavior of a deployment whose
// Secret does not carry the pooled key: it serves on the direct endpoint
// exactly as it did before the pooler existed.
func TestServingURLFallsBackToDirect(t *testing.T) {
	got := servingURL("", "postgres://direct:5432/replichai")
	if want := "postgres://direct:5432/replichai"; got != want {
		t.Fatalf("servingURL() = %q, want the direct endpoint %q", got, want)
	}
}

// TestOpenStoresChoosesMemoryOnTheDirectURL keeps the in-memory path decided by
// the direct endpoint: a Secret that carries only the pooled key names no
// database this server can migrate, so it is not a configured database.
func TestOpenStoresChoosesMemoryOnTheDirectURL(t *testing.T) {
	st, err := openStores(context.Background(), "postgres://u:p@"+poolHost+":25061/replichai-pool", "")
	if err != nil {
		t.Fatalf("openStores() = %v, want the in-memory stores", err)
	}
	defer st.close()
	if st.jobs == nil || st.access == nil {
		t.Fatal("openStores() returned no in-memory stores")
	}
}

// TestOpenStoresOpensServingOnTheFirstURL proves the first argument reaches
// pgxpool. An unparseable serving DSN fails while opening the serving pool,
// before the migration endpoint is consulted at all.
func TestOpenStoresOpensServingOnTheFirstURL(t *testing.T) {
	t.Parallel()
	_, err := openStores(context.Background(), "://not-a-dsn", "postgres://u:p@"+directHost+":5432/replichai")
	if err == nil {
		t.Fatal("openStores() = nil error, want a failure from the serving DSN")
	}
	if !strings.Contains(err.Error(), "open serving pool") {
		t.Fatalf("openStores() error = %v, want it to name the serving pool", err)
	}
}

// TestOpenStoresMigratesOnTheSecondURL proves the migration endpoint is the
// direct DSN and never the pooled one. The pooled endpoint is a transaction-mode
// pool of fixed size budgeted for request traffic; a boot-time migration on it
// queues behind serving work and holds nothing across the transactions the
// pooler reassigns a backend between. The serving DSN here is parseable and
// would connect nowhere: if it reached the migrator, the error would name the
// pooled host.
func TestOpenStoresMigratesOnTheSecondURL(t *testing.T) {
	t.Parallel()
	_, err := openStores(context.Background(), "postgres://u:p@"+poolHost+":25061/replichai-pool", "://not-a-dsn")
	if err == nil {
		t.Fatal("openStores() = nil error, want a failure from the migration DSN")
	}
	if !strings.Contains(err.Error(), "open migration pool") {
		t.Fatalf("openStores() error = %v, want it to name the migration pool", err)
	}
	if strings.Contains(err.Error(), poolHost) {
		t.Fatalf("openStores() error = %v, want the migrator never to see the pooled host %q", err, poolHost)
	}
}

// TestMigrateAllCapsItsPoolAtOneConnection holds the size of the boot-time
// claim on the direct endpoint. pgxpool's default is max(4, NumCPU), and a
// migrator taking that many would make the cutover a larger direct claim than
// the arrangement it replaces.
func TestMigrateAllCapsItsPoolAtOneConnection(t *testing.T) {
	t.Parallel()
	// connect_timeout bounds the dial; the pool is configured before anything
	// is dialed, so the cap is observable through the error's absence of a
	// second connection rather than through a live pool. Assert on the parsed
	// configuration directly instead.
	cfg, err := migrationPoolConfig("postgres://u:p@" + directHost + ":5432/replichai")
	if err != nil {
		t.Fatalf("migrationPoolConfig() = %v, want a parsed configuration", err)
	}
	if cfg.MaxConns != 1 {
		t.Fatalf("MaxConns = %d, want 1", cfg.MaxConns)
	}
	if cfg.MinConns != 0 {
		t.Fatalf("MinConns = %d, want 0", cfg.MinConns)
	}
}

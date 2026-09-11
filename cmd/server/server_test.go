// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/fetch"
)

// The reference a person types into the app is a URL this process will open.
// The server's fetcher must therefore be the guarded one: a loopback or
// private address is refused before a connection is made.
func TestServerFetcherRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<title>secret</title>"))
	}))
	defer srv.Close()

	_, err := newHTTPFetcher(time.Second).Fetch(context.Background(), srv.URL)
	if !errors.Is(err, fetch.ErrForbiddenAddress) {
		t.Fatalf("the server fetched a loopback address: err=%v", err)
	}
}

// A connection kept alive and left silent is closed, and the deadlines that
// would cut a running event stream short are left unset on purpose.
func TestServerClosesIdleConnectionsAndLeavesStreamsAlone(t *testing.T) {
	srv := newServer(":0", http.NotFoundHandler())
	if srv.IdleTimeout <= 0 {
		t.Fatal("IdleTimeout is unset: a silent kept-alive connection is held forever")
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout is unset: a client that says nothing holds a connection")
	}
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Fatal("ReadTimeout or WriteTimeout is set: it would cancel the event stream mid-run")
	}
}

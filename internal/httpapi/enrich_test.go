// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingResolver records how many resolves are in flight at once.
type countingResolver struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
	mu       sync.Mutex
}

func (c *countingResolver) Resolve(ctx context.Context, ref string) (ResolvedPaper, error) {
	n := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	c.calls.Add(1)
	c.mu.Lock()
	if n > c.peak.Load() {
		c.peak.Store(n)
	}
	c.mu.Unlock()
	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
	}
	return ResolvedPaper{Key: ref, Title: "Title of " + ref}, nil
}

// One request with many papers must not open as many connections to the paper
// sources: enrichment resolves a few at a time.
func TestTitleEnrichmentIsBounded(t *testing.T) {
	resolver := &countingResolver{}
	d := testDeps(allowAll)
	d.Resolver = resolver
	rt := NewRouter(d)

	refs := make([]string, 0, 24)
	for i := range 24 {
		refs = append(refs, fmt.Sprintf("10.1000/paper-%02d", i))
	}
	body, _ := json.Marshal(map[string]any{"title": "Many", "papers": refs})
	w := do(t, rt.Handler(), "POST", "/projects", string(body), "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", w.Code, w.Body.String())
	}
	rt.WaitBackground()

	if got := resolver.calls.Load(); got != 24 {
		t.Fatalf("resolved %d papers, want all 24", got)
	}
	if peak := resolver.peak.Load(); peak > maxConcurrentResolves {
		t.Fatalf("peak concurrency %d, want at most %d", peak, maxConcurrentResolves)
	}
}

// A request past the paper bound is refused before anything is recorded.
func TestTooManyPapersInOneRequestIsRefused(t *testing.T) {
	d := testDeps(allowAll)
	rt := NewRouter(d)
	refs := make([]string, 0, MaxPapersPerRequest+1)
	for i := range MaxPapersPerRequest + 1 {
		refs = append(refs, fmt.Sprintf("10.1000/paper-%03d", i))
	}
	body, _ := json.Marshal(map[string]any{"title": "Too many", "papers": refs})
	w := do(t, rt.Handler(), "POST", "/projects", string(body), "me")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "too_many_papers") {
		t.Fatalf("status = %d body = %s, want 400 too_many_papers", w.Code, w.Body.String())
	}
}

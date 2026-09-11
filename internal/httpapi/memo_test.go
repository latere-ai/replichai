// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/latere-ai/replichai/internal/project"
)

// publicListings counts how often the public zone is listed. This is the
// query that every anonymous registry and sitemap request used to run.
type publicListings struct {
	ProjectStore
	listed atomic.Int32
}

func (c *publicListings) ListPublic(ctx context.Context) ([]project.Project, error) {
	c.listed.Add(1)
	return c.ProjectStore.ListPublic(ctx)
}

// TestTheAnonymousRegistryAndSitemapAreBuiltOncePerChange checks that the
// anonymous registry and the sitemap, which are the same for every caller who
// is not signed in, are built once and reused rather than built on each
// request, and that they are rebuilt as soon as a publication changes, so a
// change in visibility is visible to anonymous callers at once.
func TestTheAnonymousRegistryAndSitemapAreBuiltOncePerChange(t *testing.T) {
	d := testDeps(allowAll)
	store := &publicListings{ProjectStore: d.Projects}
	d.Projects = store
	rt := NewRouter(d)
	h := rt.Handler()
	ctx := context.Background()

	p, err := d.Projects.Create(ctx, "owner", "Public work")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Projects.SetPublic(ctx, p.ID, project.SignOff{By: "owner", AttestationVersion: "1"}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if w := do(t, h, "GET", "/public", "", ""); w.Code != http.StatusOK {
			t.Fatalf("anonymous /public: %d %s", w.Code, w.Body.String())
		}
	}
	if got := store.listed.Load(); got != 1 {
		t.Fatalf("three anonymous registry hits listed the public zone %d times, want once", got)
	}
	for range 3 {
		if w := do(t, h, "GET", "/sitemap.xml", "", ""); w.Code != http.StatusOK {
			t.Fatalf("sitemap: %d", w.Code)
		}
	}
	if got := store.listed.Load(); got != 2 {
		t.Fatalf("three sitemap hits listed the public zone %d more times, want once", got-1)
	}

	// A signed-in caller's list carries their own relation to each row and is
	// built for them, so the memoised answer is not used.
	if w := do(t, h, "GET", "/public", "", "someone"); w.Code != http.StatusOK {
		t.Fatalf("signed-in /public: %d", w.Code)
	}
	if got := store.listed.Load(); got != 3 {
		t.Fatalf("a signed-in list must be built for the caller; listed %d times, want 3", got)
	}

	// A change to a publication drops both memoised answers.
	rt.publicChanged()
	_ = do(t, h, "GET", "/public", "", "")
	_ = do(t, h, "GET", "/sitemap.xml", "", "")
	if got := store.listed.Load(); got != 5 {
		t.Fatalf("after a change both answers are rebuilt; listed %d times, want 5", got)
	}
}

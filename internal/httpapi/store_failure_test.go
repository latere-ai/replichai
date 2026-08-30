// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
)

// errStore is the failure every store shares in these tests. It is deliberately
// not one of the domain's sentinel errors: a backend that is simply down must
// not be mistaken for a "not found" or a policy refusal.
var errStore = errors.New("store is unavailable")

// A read path that cannot reach its store must answer 500, never 200 with an
// empty body and never 404. The distinction is the whole point: a 404 tells a
// caller the thing does not exist, and a broken database does not know that.
//
// These paths were the largest untested surface in this package — every one of
// them is a branch that only runs when persistence fails, which is exactly when
// being wrong is most expensive.
type failingProjects struct {
	project.Store
	getErr, listErr, publicErr, setErr, titleErr error
}

func (f failingProjects) Get(ctx context.Context, id string) (project.Project, error) {
	if f.getErr != nil {
		return project.Project{}, f.getErr
	}
	return f.Store.Get(ctx, id)
}

func (f failingProjects) ListByOwner(ctx context.Context, owner string) ([]project.Project, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.Store.ListByOwner(ctx, owner)
}

func (f failingProjects) ListPublic(ctx context.Context) ([]project.Project, error) {
	if f.publicErr != nil {
		return nil, f.publicErr
	}
	return f.Store.ListPublic(ctx)
}

func (f failingProjects) SetPublic(ctx context.Context, id string, s project.SignOff) error {
	if f.setErr != nil {
		return f.setErr
	}
	return f.Store.SetPublic(ctx, id, s)
}

func (f failingProjects) SetPrivate(ctx context.Context, id string, w project.Withdrawal) error {
	if f.setErr != nil {
		return f.setErr
	}
	return f.Store.SetPrivate(ctx, id, w)
}

func (f failingProjects) SetTitle(ctx context.Context, id, title string) error {
	if f.titleErr != nil {
		return f.titleErr
	}
	return f.Store.SetTitle(ctx, id, title)
}

type failingJobs struct {
	job.Store
	listErr, statusErr error
}

func (f failingJobs) ListByPrincipal(ctx context.Context, p string) ([]job.Job, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.Store.ListByPrincipal(ctx, p)
}

func (f failingJobs) ListByProject(ctx context.Context, id string) ([]job.Job, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.Store.ListByProject(ctx, id)
}

func (f failingJobs) StatusByProjects(ctx context.Context, ids []string) (map[string]job.ProjectStatus, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	return f.Store.StatusByProjects(ctx, ids)
}

type failingMembers struct {
	access.Membership
	listErr, addErr, removeErr, seedErr, projectsErr error
}

func (f failingMembers) List(ctx context.Context, p string) ([]access.Member, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.Membership.List(ctx, p)
}

func (f failingMembers) Add(ctx context.Context, by access.Principal, p, email string, r access.Role) error {
	if f.addErr != nil {
		return f.addErr
	}
	return f.Membership.Add(ctx, by, p, email, r)
}

func (f failingMembers) Remove(ctx context.Context, by access.Principal, p, email string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.Membership.Remove(ctx, by, p, email)
}

func (f failingMembers) SeedLead(ctx context.Context, p string, lead access.Principal) error {
	if f.seedErr != nil {
		return f.seedErr
	}
	return f.Membership.SeedLead(ctx, p, lead)
}

func (f failingMembers) Projects(ctx context.Context, sub string) ([]string, error) {
	if f.projectsErr != nil {
		return nil, f.projectsErr
	}
	return f.Membership.Projects(ctx, sub)
}

type failingProfiles struct {
	profile.Store
	getErr, saveErr error
}

func (f failingProfiles) Get(ctx context.Context, p string) (profile.Profile, error) {
	if f.getErr != nil {
		return profile.Profile{}, f.getErr
	}
	return f.Store.Get(ctx, p)
}

func (f failingProfiles) Save(ctx context.Context, p profile.Profile) (profile.Profile, error) {
	if f.saveErr != nil {
		return profile.Profile{}, f.saveErr
	}
	return f.Store.Save(ctx, p)
}

// A store that cannot be read is a 500 on every read surface.
func TestReadsReportAStoreFailureAsFiveHundred(t *testing.T) {
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		deps   func(Deps, string) Deps
	}{
		{"list projects", "GET", "/projects", "", func(d Deps, _ string) Deps {
			d.Projects = failingProjects{Store: d.Projects.(project.Store), listErr: errStore}
			return d
		}},
		{"list projects with a broken session count", "GET", "/projects", "", func(d Deps, _ string) Deps {
			d.Jobs = failingJobs{Store: d.Jobs.(job.Store), statusErr: errStore}
			return d
		}},
		{"list public", "GET", "/public", "", func(d Deps, _ string) Deps {
			d.Projects = failingProjects{Store: d.Projects.(project.Store), publicErr: errStore}
			return d
		}},
		{"list sessions", "GET", "/jobs", "", func(d Deps, _ string) Deps {
			d.Jobs = failingJobs{Store: d.Jobs.(job.Store), listErr: errStore}
			return d
		}},
		{"read a project", "GET", "/projects/{id}", "", func(d Deps, _ string) Deps {
			d.Jobs = failingJobs{Store: d.Jobs.(job.Store), listErr: errStore}
			return d
		}},
		{"list members", "GET", "/projects/{id}/members", "", func(d Deps, _ string) Deps {
			d.Members = failingMembers{Membership: d.Members.(access.Membership), listErr: errStore}
			return d
		}},
		{"list projects when the membership walk fails", "GET", "/projects", "", func(d Deps, _ string) Deps {
			d.Members = failingMembers{Membership: d.Members.(access.Membership), projectsErr: errStore}
			return d
		}},
		{"read a profile", "GET", "/me/profile", "", func(d Deps, _ string) Deps {
			d.Profiles = failingProfiles{Store: d.Profiles.(profile.Store), getErr: errStore}
			return d
		}},
		{"save a profile", "PATCH", "/me/profile", `{"display_name":"Ada"}`, func(d Deps, _ string) Deps {
			d.Profiles = failingProfiles{Store: d.Profiles.(profile.Store), saveErr: errStore}
			return d
		}},
		{"rename a project", "PATCH", "/projects/{id}", `{"title":"New"}`, func(d Deps, _ string) Deps {
			d.Projects = failingProjects{Store: d.Projects.(project.Store), titleErr: errStore}
			return d
		}},
		{"add a member", "POST", "/projects/{id}/members", `{"email":"c@x.com"}`, func(d Deps, _ string) Deps {
			d.Members = failingMembers{Membership: d.Members.(access.Membership), addErr: errStore}
			return d
		}},
		{"seed the lead before adding", "POST", "/projects/{id}/members", `{"email":"c@x.com"}`, func(d Deps, _ string) Deps {
			d.Members = failingMembers{Membership: d.Members.(access.Membership), seedErr: errStore}
			return d
		}},
		{"remove a member", "DELETE", "/projects/{id}/members/c@x.com", "", func(d Deps, _ string) Deps {
			d.Members = failingMembers{Membership: d.Members.(access.Membership), removeErr: errStore}
			return d
		}},
		{"seed the lead before removing", "DELETE", "/projects/{id}/members/c@x.com", "", func(d Deps, _ string) Deps {
			d.Members = failingMembers{Membership: d.Members.(access.Membership), seedErr: errStore}
			return d
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDeps(allowAll)
			p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")
			d = tt.deps(d, p.ID)
			h := NewRouter(d).Handler()

			path := tt.path
			if path == "/projects/{id}" || len(path) > 12 && path[:11] == "/projects/{" {
				path = replaceID(tt.path, p.ID)
			}
			w := doAs(t, h, tt.method, path, tt.body, owner)
			if w.Code != http.StatusInternalServerError {
				t.Errorf("%s %s = %d, want 500 when the store is down", tt.method, path, w.Code)
			}
		})
	}
}

// replaceID substitutes the project id into a route pattern.
func replaceID(pattern, id string) string {
	out := ""
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '{' {
			j := i
			for j < len(pattern) && pattern[j] != '}' {
				j++
			}
			out += id
			i = j
			continue
		}
		out += string(pattern[i])
	}
	return out
}

// A project that cannot be read is a 500, distinct from one that is absent
// (404) — hiding a broken database behind "not found" would send an owner
// looking for a project they never lost.
func TestAReadableProjectIsDistinctFromAReachableStore(t *testing.T) {
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	d := testDeps(allowAll)
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")
	d.Projects = failingProjects{Store: d.Projects.(project.Store), getErr: errStore}
	h := NewRouter(d).Handler()

	for _, path := range []string{
		"/projects/" + p.ID,
		"/projects/" + p.ID + "/public",
		"/projects/" + p.ID + "/private",
	} {
		method := "GET"
		if path != "/projects/"+p.ID {
			method = "POST"
		}
		if w := doAs(t, h, method, path, `{"attestation_version":"v2","signed_off_contact":"a@b.com"}`, owner); w.Code != http.StatusInternalServerError {
			t.Errorf("%s %s = %d, want 500", method, path, w.Code)
		}
	}
}

// The publish and withdraw writes fail loudly too: a sign-off that was not
// recorded must never be reported as recorded.
func TestPublishAndWithdrawReportAWriteFailure(t *testing.T) {
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}

	d := testDeps(allowAll)
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")
	publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	publishable(t, d, owner.Sub, "ada")
	d.Projects = failingProjects{Store: d.Projects.(project.Store), setErr: errStore}
	h := NewRouter(d).Handler()

	body := `{"attestation_version":"v2","signed_off_name":"Ada","signed_off_contact":"ada@x.com"}`
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", body, owner); w.Code != http.StatusInternalServerError {
		t.Errorf("publish with a failing write = %d, want 500", w.Code)
	}
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/private", `{"reason":"oops"}`, owner); w.Code != http.StatusInternalServerError {
		t.Errorf("withdraw with a failing write = %d, want 500", w.Code)
	}
}

// A project whose sessions cannot be listed cannot be cleared for publication:
// the gate reads the manifests, and an unread manifest is not a clear one.
func TestPublishRefusesWhenSessionsCannotBeRead(t *testing.T) {
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	d := testDeps(allowAll)
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")
	publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	publishable(t, d, owner.Sub, "ada")
	d.Jobs = failingJobs{Store: d.Jobs.(job.Store), listErr: errStore}
	h := NewRouter(d).Handler()

	body := `{"attestation_version":"v2","signed_off_name":"Ada","signed_off_contact":"ada@x.com"}`
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", body, owner); w.Code != http.StatusInternalServerError {
		t.Errorf("publish with unreadable sessions = %d, want 500", w.Code)
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/authkit/jwt"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/cloud"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
	"github.com/latere-ai/replichai/internal/webauth"
)

const chainSub, chainEmail = "operator-sub", "operator@example.test"

// startApp is the deployment the terminal talks to: the real router, the real
// gate, and the real bearer middleware over a validator that accepts the actor
// token the auth stub below mints. It returns the app and the auth service.
func startApp(t *testing.T, provisioner httpapi.RepoProvisioner) (app, auth *httptest.Server) {
	t.Helper()
	ctx := t.Context()
	acc := access.NewMemStore()
	if err := acc.SeedAdmin(ctx, chainEmail); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	jobs := job.NewMemStore()
	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:        jobs,
		Projects:    project.NewMemStore(project.WithSessions(jobs)),
		Repos:       provisioner,
		Gate:        webauth.Gate(acc),
		SSEInterval: 10 * time.Millisecond,
	})
	app = httptest.NewServer(webauth.Bearer(actorTokens{})(router.Handler()))
	t.Cleanup(app.Close)

	auth = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actor-tokens" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"actor_token": "actor." + chainSub})
	}))
	t.Cleanup(auth.Close)
	return app, auth
}

// actorTokens accepts the audience-scoped token the auth service minted. The
// signature is the auth service's business; what this test pins is that the
// terminal presents a credential the app's bearer path accepts.
type actorTokens struct{}

func (actorTokens) Validate(raw string) (*jwt.Claims, error) {
	if raw != "actor."+chainSub {
		return nil, jwt.ErrInvalidSignature
	}
	return &jwt.Claims{Sub: chainSub, Email: chainEmail}, nil
}

// signIn writes the credential `replichai login` would have written.
func signIn(t *testing.T, home string) {
	t.Helper()
	err := cloud.DefaultStore(home).Save(cloud.Token{
		AccessToken: "access-token", Issuer: "https://auth.example.test",
		ClientID: "replichai-cli", Subject: chainSub, Email: chainEmail,
		Expiry: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}
}

// scriptedRunner is what `replichai run` does to one paper, over a sandbox
// driver that runs no process. The audit, the runtime and the batch are the
// product's; only the driver is scripted.
type scriptedRunner struct {
	t      *testing.T
	root   string
	out    string
	pub    *publisher
	pushed string
}

func (*scriptedRunner) Prepare(context.Context, batch.Resolved, batch.EntryState) error { return nil }

func (r *scriptedRunner) Execute(ctx context.Context, entry batch.Resolved) (batch.Outcome, error) {
	registry, err := harness.NewRegistry(harness.NewClaudeCode())
	if err != nil {
		return batch.Outcome{}, err
	}
	catalog, err := local.NewCatalog(local.DefaultCatalogFile(), registry,
		func(string) (string, bool) { return "", false })
	if err != nil {
		return batch.Outcome{}, err
	}
	runtime, err := local.New(local.Config{
		Sandbox: localtest.New(localtest.Script{WorkerCommits: true}),
		Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: r.root, Environ: os.Environ(),
	})
	if err != nil {
		return batch.Outcome{}, err
	}
	router, err := runtimes.NewRouter(managed.RuntimeName("local"), runtime)
	if err != nil {
		return batch.Outcome{}, err
	}
	policy := managed.DefaultWaitPolicy()
	policy.InitialDelay, policy.MaxDelay = time.Millisecond, time.Millisecond
	controller := managed.NewController(router, managed.WithWaitPolicy(policy))

	m, err := method.Load(writeMethod(r.t))
	if err != nil {
		return batch.Outcome{}, err
	}
	capabilities, err := runtime.Capabilities(ctx)
	if err != nil {
		return batch.Outcome{}, err
	}
	// The order the product keeps: the workspace holds the only copy of the
	// work until the push has taken it, so the push is a retain hook and not
	// something done after the session is released.
	hooks := managed.Hooks{Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
		out, errOut := newTemp(r.t), newTemp(r.t)
		if err := pushWorkspace(ctx, r.pub, capabilities.OutputDir,
			entry.Reference(), evidence.Status.Ref, out, errOut); err != nil {
			return err
		}
		r.pushed = capabilities.OutputDir
		return nil
	}}
	auditor := audit.NewSessionAuditor(controller, m,
		audit.WithSessionMaxRevisions(1),
		audit.WithLocalWorkspace(),
		audit.WithSessionHooks(hooks),
	)
	result, err := auditor.Run(ctx, entry.Reference(), r.out)
	if err != nil {
		return batch.Outcome{}, err
	}
	workspace := r.pushed
	grade := ""
	if result.Status.Verdict != nil {
		grade = string(result.Status.Verdict.Grade)
	}
	return batch.Outcome{
		Session: result.Ref.ID, Directory: workspace, Grade: grade,
		Model: result.Status.Model, Usage: result.Status.Usage, Ref: result.Ref,
	}, nil
}

// bareRepo is a git remote on this disk. LocalPush pushes to it exactly as it
// would to GitHub: the same command, a different URL.
func bareRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", "--initial-branch=main", dir)
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	command := exec.Command("git", args...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return git(t, dir, args...)
}

func get(t *testing.T, url string) string {
	t.Helper()
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	request.Header.Set("Authorization", "Bearer actor."+chainSub)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode/100 != 2 {
		t.Fatalf("GET %s: %s: %s", url, response.Status, body)
	}
	return string(body)
}

// fakeForge answers the REST calls the evidence commit makes, so the step that
// follows the push is exercised rather than skipped.
func fakeForge(t *testing.T) *httptest.Server {
	t.Helper()
	var committed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Count(strings.Trim(path, "/"), "/") == 2 && r.Method == http.MethodGet:
			write(w, http.StatusOK, map[string]any{"full_name": "open-hai/a-paper-a3f9", "private": true})
		case strings.HasSuffix(path, "/commits") && r.Method == http.MethodGet:
			write(w, http.StatusOK, []map[string]string{{"sha": "deadbeef"}})
		case strings.Contains(path, "/git/trees/") && r.Method == http.MethodGet:
			write(w, http.StatusOK, map[string]any{"tree": []any{}, "truncated": false})
		case strings.Contains(path, "/contents/") && r.Method == http.MethodPut:
			committed = append(committed, strings.SplitN(path, "/contents/", 2)[1])
			write(w, http.StatusCreated, map[string]any{"content": map[string]string{"sha": "manifest"}})
		case strings.Contains(path, "/contents/"):
			http.NotFound(w, r) // nothing is already committed
		case strings.HasSuffix(path, "/git/blobs"):
			write(w, http.StatusCreated, map[string]string{"sha": "blob"})
		case strings.Contains(path, "/git/ref/heads/"):
			write(w, http.StatusOK, map[string]any{"object": map[string]string{"sha": "deadbeef"}})
		case strings.Contains(path, "/git/commits/"):
			write(w, http.StatusOK, map[string]any{"tree": map[string]string{"sha": "tree"}})
		case strings.HasSuffix(path, "/git/trees"):
			var body struct {
				Tree []struct {
					Path string `json:"path"`
				} `json:"tree"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, entry := range body.Tree {
				committed = append(committed, entry.Path)
			}
			write(w, http.StatusCreated, map[string]string{"sha": "newtree"})
		case strings.HasSuffix(path, "/git/commits"):
			write(w, http.StatusCreated, map[string]string{"sha": "newcommit"})
		case strings.Contains(path, "/git/refs/heads/"):
			write(w, http.StatusOK, map[string]any{"object": map[string]string{"sha": "newcommit"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		if len(committed) == 0 {
			t.Error("the system committed no evidence after the push")
		}
	})
	return server
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// bareRepoProvisioner is the platform's half of 045l with the forge replaced by
// a bare repository on this disk: it allocates a name, hands back a credential
// with an end, and records what it issued so the test can assert the terminal
// pushed with that credential and gave it back afterwards.
type bareRepoProvisioner struct {
	remote  string
	issued  string
	expires time.Time
	fail    error
}

func (p *bareRepoProvisioner) Provision(_ context.Context, id paper.Identity) (httpapi.Provisioned, error) {
	if p.fail != nil {
		return httpapi.Provisioned{}, p.fail
	}
	p.issued = "ghs_scoped_" + id.Key
	return httpapi.Provisioned{
		Repo: "open-hai/a-paper-a3f9", URL: p.remote,
		Token: p.issued, Expires: p.expires,
	}, nil
}

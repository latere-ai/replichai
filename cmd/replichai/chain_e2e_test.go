package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

// The whole local path, in one test: open a project in the app, run a paper on
// this machine, push what the agent committed to a git remote, and file the
// record back under the project.
//
// Every step is covered somewhere already, and none of that proved the four
// steps reach each other. The first run of this test found that they did not:
// the terminal asked for /api/projects and the app serves /projects, so every
// command that talks to the app answered 404 against a real deployment.
//
// What is scripted here is one thing: the sandbox driver. Everything else —
// the router, the credential, the actor token, the batch, the audit, the git
// push — is the code that ships.
func TestAProjectIsOpenedRunPushedAndFiled(t *testing.T) {
	app, auth := startApp(t)
	home := t.TempDir()
	signIn(t, home)
	t.Setenv("HOME", home)
	t.Setenv("REPLICHAI_API_URL", app.URL)
	t.Setenv("REPLICHAI_AUTH_URL", auth.URL)

	// 1. Open a project, and find it in the list.
	stdout, stderr, err := capture(t, "project", "create", "Papers from my laptop")
	if err != nil {
		t.Fatalf("project create: %v\n%s", err, stderr)
	}
	projectID, title, _ := strings.Cut(strings.TrimSpace(stdout), "\t")
	if projectID == "" || title != "Papers from my laptop" {
		t.Fatalf("project create printed %q", stdout)
	}
	listed, _, err := capture(t, "project", "ls")
	if err != nil {
		t.Fatalf("project ls: %v", err)
	}
	if !strings.Contains(listed, projectID) || !strings.Contains(listed, title) {
		t.Fatalf("project ls does not list the project it just made:\n%s", listed)
	}

	// 2. Run the paper here, and 3. push what the agent committed as the run
	// ends. Those are one step in the product and one step here: the workspace
	// holds the only copy of the work until the push has taken it, so the push
	// is a retain hook rather than something done to a released session.
	//
	// The remote is a bare repository rather than GitHub: the same git push
	// over a different URL. The forge API the evidence commit uses is answered
	// in process.
	remote := bareRepo(t)
	pub := &publisher{
		gh:    github.New("test-token", github.WithBaseURL(fakeForge(t).URL)),
		owner: "open-hai", name: "a-paper-a3f9",
		mount: provider.RepoMount{URL: remote, Token: "test-token"},
	}
	work := t.TempDir()
	state, err := batch.Run(t.Context(),
		batch.File{Project: title, Papers: []batch.Entry{{Paper: "10.1145/3654777.3676417"}}},
		&scriptedRunner{t: t, root: t.TempDir(), out: work, pub: pub},
		batch.Options{StatePath: filepath.Join(work, "batch.json")})
	if err != nil {
		t.Fatalf("run the project: %v: %s", err, state.Entries[0].Error)
	}
	if len(state.Entries) != 1 || state.Entries[0].Phase != batch.Done {
		t.Fatalf("the paper did not finish: %+v", state.Entries)
	}
	pushed := gitOut(t, remote, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(pushed, "REPRODUCIBILITY.md") {
		t.Errorf("the remote did not receive the reproduction: %q", pushed)
	}

	// The gate is asked what it makes of that tree, so what publication would
	// allow is pinned rather than incidental.
	if flagged := publish.VerifyTree(strings.Fields(pushed)); len(flagged) != 0 {
		t.Errorf("a scripted reproduction must verify clean, flagged %v", flagged)
	}

	// 4. File the record under the project.
	stdout, stderr, err = capture(t, "push", filepath.Join(work, "batch.json"), "--project", projectID)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "pushed 1,") {
		t.Fatalf("push reported %q / %q", stdout, stderr)
	}

	// Pushing again is the documented way to finish an interrupted upload, so
	// it must file the same session rather than refuse the paper it already
	// filed or open a second row.
	if _, stderr, err = capture(t, "push", filepath.Join(work, "batch.json"), "--project", projectID); err != nil {
		t.Fatalf("push again: %v\n%s", err, stderr)
	}

	// And the app holds it, as an ordinary session that records where it ran.
	jobs := get(t, app.URL+"/jobs")
	if !strings.Contains(jobs, `"origin":"`+string(job.OriginLocal)+`"`) {
		t.Errorf("the app does not hold the imported session: %s", jobs)
	}
	if !strings.Contains(jobs, projectID) {
		t.Errorf("the session is not filed under the project: %s", jobs)
	}
	if count := strings.Count(jobs, `"origin":"`+string(job.OriginLocal)+`"`); count != 1 {
		t.Errorf("two pushes opened %d sessions, want 1:\n%s", count, jobs)
	}
}

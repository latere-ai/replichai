// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"time"

	"path/filepath"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/cloud"

	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

// TestAProjectIsOpenedRunPushedAndFiled covers the whole local path in one
// test: open a project in the app, run a paper on this machine, push what the
// agent committed to a git remote, and file the record under the project.
//
// Every step is covered elsewhere already, but none of that proved the four
// steps connect. The first run of this test found that they did not: the
// terminal requested /api/projects and the app serves /projects, so every
// command that talks to the app answered 404 against a real deployment.
//
// Only the sandbox driver is scripted. The router, the credential, the actor
// token, the batch, the audit and the git push are the code that ships.
func TestAProjectIsOpenedRunPushedAndFiled(t *testing.T) {
	remote := bareRepo(t)
	provisioner := &bareRepoProvisioner{remote: remote, expires: time.Now().Add(time.Hour)}
	app, auth := startApp(t, provisioner)
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
	// ends. These are one step in the product and one step here: the workspace
	// holds the only copy of the work until the push has taken it, so the push
	// is a retain hook rather than something done to a released session.
	//
	// The repository and the credential come from the platform (045l): the
	// terminal never names either, and holds no organization token. The remote
	// is a bare repository rather than GitHub, which is the same git push over
	// a different URL, and the forge API the evidence commit uses is answered
	// in process.
	api := cloud.NewClient(cloud.New(home, auth.URL), app.URL)
	if err := api.AddPaper(t.Context(), projectID, "10.1145/3654777.3676417"); err != nil {
		t.Fatalf("file the paper: %v", err)
	}
	allocated, err := api.ProvisionRepository(t.Context(), projectID, "doi:10.1145/3654777.3676417")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if allocated.Token != provisioner.issued || allocated.Repo != "open-hai/a-paper-a3f9" {
		t.Fatalf("provisioned = %+v", allocated)
	}
	if allocated.Expires.IsZero() {
		t.Error("a credential handed out with no end is not a short-lived one")
	}
	owner, name, _ := strings.Cut(allocated.Repo, "/")
	pub := &publisher{
		gh:    github.New(allocated.Token, github.WithBaseURL(fakeForge(t).URL)),
		owner: owner, name: name,
		mount: provider.RepoMount{URL: allocated.URL, Token: allocated.Token},
	}
	work := t.TempDir()
	runner := &scriptedRunner{t: t, root: t.TempDir(), out: work, pub: pub}
	state, err := batch.Run(t.Context(),
		batch.File{Project: title, Papers: []batch.Entry{{Paper: "10.1145/3654777.3676417"}}},
		runner,
		batch.Options{StatePath: filepath.Join(work, "batch.json")})
	if err != nil {
		t.Fatalf("run the project: %v: %s", err, state.Entries[0].Error)
	}

	// The run's record is read from the session directory the runtime wrote,
	// not from a fixture: the harness and the sandbox it names are the ones
	// that ran, and the round is the one grade this session kept.
	record := runner.record
	if record.Runtime != "local" || record.Harness != "claude-code" || record.Sandbox == "" || record.Network == "" {
		t.Errorf("the record does not say what the run ran on: %+v", record)
	}
	if record.State != "done" || record.Rounds != 1 || record.Model == nil || !strings.HasPrefix(record.PromptDigest, "sha256:") {
		t.Errorf("the record does not say how the run went: %+v", record)
	}
	if len(state.Entries) != 1 || state.Entries[0].Phase != batch.Done {
		t.Fatalf("the paper did not finish: %+v", state.Entries)
	}
	pushed := gitOut(t, remote, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(pushed, "REPRODUCIBILITY.md") {
		t.Errorf("the remote did not receive the reproduction: %q", pushed)
	}

	// The publish gate is run over that tree, so what publication would allow
	// is asserted rather than incidental.
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

	// The app holds it as an ordinary session that records where it ran.
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

// TestADeploymentThatCannotProvisionFallsBackWithAReason covers 045l AC4 on
// the terminal's side: a deployment that cannot mint names the setting it is
// missing, and the run continues on the credential this machine holds rather
// than refusing because of the deployment's configuration.
func TestADeploymentThatCannotProvisionFallsBackWithAReason(t *testing.T) {
	app, auth := startApp(t, &bareRepoProvisioner{fail: httpapi.ErrMintingUnavailable})
	home := t.TempDir()
	signIn(t, home)
	t.Setenv("HOME", home)
	t.Setenv("REPLICHAI_API_URL", app.URL)
	t.Setenv("REPLICHAI_AUTH_URL", auth.URL)
	t.Setenv("REPLICHAI_GITHUB_TOKEN", "")

	stdout, _, err := capture(t, "project", "create", "Papers from my laptop")
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	projectID, _, _ := strings.Cut(strings.TrimSpace(stdout), "\t")

	errFile := newTemp(t)
	_, err = publisherFor(t.Context(), projectID, "open-hai", "10.1145/3654777.3676417",
		"sha256:x", "", 5*time.Second, errFile)
	said := readAll(t, errFile)
	if !strings.Contains(said, "REPLICHAI_GITHUB_APP_ID") {
		t.Errorf("the terminal did not name the missing setting:\n%s", said)
	}
	// With no credential of its own either, the terminal refuses and names
	// both ways of supplying one.
	if err == nil || !strings.Contains(err.Error(), "REPLICHAI_GITHUB_TOKEN") {
		t.Errorf("fallback with no token = %v, want it to name what is missing", err)
	}
}

// TestInvitingNeedsACredentialThatCanAdminister checks that, because a minted
// credential carries contents:write and nothing else, inviting a collaborator
// is refused before anything is created rather than after the reproduction has
// been pushed.
func TestInvitingNeedsACredentialThatCanAdminister(t *testing.T) {
	_, err := publisherFor(t.Context(), "some-project", "open-hai", "10.1145/3654777.3676417",
		"sha256:x", "a-collaborator", 5*time.Second, newTemp(t))
	if err == nil || !strings.Contains(err.Error(), "--invite") {
		t.Fatalf("publisherFor with --invite = %v, want a refusal naming the flag", err)
	}
}

// TestSessionsAreListedAndThrownAwayFromTheTerminal checks that a session
// filed from a terminal can be listed and deleted from one (032). Until this
// existed the terminal could create sessions in the app but never remove one,
// so a run that failed in a way that produced several rows left the operator
// to delete each row in the browser, or to ask somebody else to.
func TestSessionsAreListedAndThrownAwayFromTheTerminal(t *testing.T) {
	remote := bareRepo(t)
	app, auth := startApp(t, &bareRepoProvisioner{remote: remote, expires: time.Now().Add(time.Hour)})
	home := t.TempDir()
	signIn(t, home)
	t.Setenv("HOME", home)
	t.Setenv("REPLICHAI_API_URL", app.URL)
	t.Setenv("REPLICHAI_AUTH_URL", auth.URL)

	stdout, stderr, err := capture(t, "project", "create", "Papers from my laptop")
	if err != nil {
		t.Fatalf("project create: %v\n%s", err, stderr)
	}
	projectID, _, _ := strings.Cut(strings.TrimSpace(stdout), "\t")

	api := cloud.NewClient(cloud.New(home, auth.URL), app.URL)
	if err := api.AddPaper(t.Context(), projectID, "10.1145/3654777.3676417"); err != nil {
		t.Fatalf("file the paper: %v", err)
	}
	id, err := api.ImportSession(t.Context(), projectID, cloud.ImportRecord{
		PaperKey: "doi:10.1145/3654777.3676417", Ref: "10.1145/3654777.3676417",
		Runtime: "local", SessionID: "abc123", Phase: "failed",
		Model: cloud.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	stdout, stderr, err = capture(t, "project", "sessions", projectID)
	if err != nil {
		t.Fatalf("project sessions: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, id) || !strings.Contains(stdout, "failed") {
		t.Fatalf("the session it just filed is not listed:\n%s", stdout)
	}

	if stdout, stderr, err = capture(t, "project", "rm-session", id); err != nil {
		t.Fatalf("rm-session: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "deleted") {
		t.Fatalf("rm-session said %q", stdout)
	}
	if stdout, _, err = capture(t, "project", "sessions", projectID); err != nil {
		t.Fatalf("project sessions after delete: %v", err)
	}
	if strings.Contains(stdout, id) {
		t.Fatalf("the deleted session is still listed:\n%s", stdout)
	}

	// A second delete is refused rather than accepted, and the refusal names
	// the id, because deleting several at once must say which one failed.
	if _, stderr, err = capture(t, "project", "rm-session", id); err == nil {
		t.Fatal("deleting an absent session was accepted")
	}
	if !strings.Contains(stderr, id) {
		t.Fatalf("the refusal does not name the session: %q", stderr)
	}
}

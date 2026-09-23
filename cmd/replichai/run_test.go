// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

// fakeRepositories stands in for GitHub under the project runner: it records
// what Prepare asked for and answers with publishers over the in-process
// forge, so the whole of `run --publish` is driven without a network.
type fakeRepositories struct {
	forge      *httptest.Server
	allocated  []string
	reattached []string
}

func (f *fakeRepositories) Allocate(_ context.Context, paperRef string) (*publisher, error) {
	f.allocated = append(f.allocated, paperRef)
	return f.publisher("open-hai", "fresh-"+slugify(paperRef)), nil
}

func (f *fakeRepositories) Reattach(_ context.Context, repo, _ string) (*publisher, error) {
	f.reattached = append(f.reattached, repo)
	owner, name, _ := strings.Cut(repo, "/")
	return f.publisher(owner, name), nil
}

func (f *fakeRepositories) publisher(owner, name string) *publisher {
	return &publisher{
		gh: github.New("t", github.WithBaseURL(f.forge.URL)), owner: owner, name: name,
		id:    paper.Identity{Key: "10.1145/3654777.3676417", Scheme: paper.SchemeDOI, Slug: "a-paper"},
		mount: provider.RepoMount{URL: "https://github.com/" + owner + "/" + name, Token: "t", MountPath: audit.DefaultMountPath},
	}
}

// testProjectRunner builds the projectRunner that `replichai run --publish`
// would build, over the memory runtime and a repository source that makes no
// network calls.
func testProjectRunner(t *testing.T, repos repositorySource) *projectRunner {
	t.Helper()
	m, err := method.Load(writeMethod(t))
	if err != nil {
		t.Fatalf("method: %v", err)
	}
	return &projectRunner{
		method: m, runtimeName: "memory", out: t.TempDir(),
		owner: "open-hai", publish: true, timeout: time.Minute, maxRevisions: 1,
		stdout: newTemp(t), stderr: newTemp(t),
		publishers: make(map[string]*publisher), repositories: repos,
	}
}

// TestRunDrivesAProjectAndWritesItsReport runs `replichai run` end to end on
// the memory runtime: the project file is read, every paper runs through the
// project runner, and the state and the report are written beside the file.
// Until this test existed nothing drove runRun, which is how a project file's
// publish block could be dead configuration without anybody noticing.
func TestRunDrivesAProjectAndWritesItsReport(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "papers.txt")
	if err := os.WriteFile(project, []byte("# two papers\nhttps://arxiv.org/abs/2603.01795\n10.1145/3654777.3676417\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	stdout, stderr, err := capture(t, "run", project,
		"--runtime", "memory", "--method", writeMethod(t), "--out", filepath.Join(dir, "runs"),
		"--interval", "0", "--parallel", "2")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "2 papers: 2 done, 0 failed") {
		t.Fatalf("the report does not account for both papers:\n%s", stdout)
	}
	// The state and the report are beside the project file, and the report
	// is the same table the terminal printed.
	state, err := batch.LoadState(filepath.Join(dir, "batch.json"))
	if err != nil {
		t.Fatalf("batch.json: %v", err)
	}
	if state.Summarize().Done != 2 {
		t.Fatalf("state = %+v", state.Summarize())
	}
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatalf("report.md: %v", err)
	}
	if string(report) != state.Report() {
		t.Fatalf("report.md is not the final report:\n%s", report)
	}
	for _, entry := range state.Entries {
		if entry.Directory == "" || !strings.HasPrefix(entry.Directory, filepath.Join(dir, "runs")) {
			t.Errorf("%s ran outside --out: %q", entry.Paper, entry.Directory)
		}
	}
}

// TestThePublishBlockResolvesInOneOrder checks that the publish block is
// resolved in one order, the flag, then the environment, then the project
// file, then the default, and that every step of it is reachable. The -owner
// flag used to default to the environment or "open-hai", so it was never empty
// and the project file's owner was dead configuration.
func TestThePublishBlockResolvesInOneOrder(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(key string) string { return vars[key] }
	}
	file := batch.PublishConfig{Owner: "from-file", Invite: "file-collaborator"}
	cases := []struct {
		name               string
		ownerFlag, invite  string
		env                map[string]string
		file               batch.PublishConfig
		wantOwner, wantInv string
	}{
		{"the flag beats everything", "from-flag", "flag-collaborator",
			map[string]string{"REPLICHAI_GITHUB_OWNER": "from-env"}, file, "from-flag", "flag-collaborator"},
		{"the environment beats the file", "", "",
			map[string]string{"REPLICHAI_GITHUB_OWNER": "from-env"}, file, "from-env", "file-collaborator"},
		{"the file beats the default", "", "", nil, file, "from-file", "file-collaborator"},
		{"nothing set is the default and nobody invited", "", "", nil, batch.PublishConfig{}, publish.DefaultOrg, ""},
		{"whitespace is unset", "  ", " ", map[string]string{"REPLICHAI_GITHUB_OWNER": " "}, file, "from-file", "file-collaborator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, invite := resolvePublish(tc.ownerFlag, tc.invite, env(tc.env), tc.file)
			if owner != tc.wantOwner || invite != tc.wantInv {
				t.Fatalf("resolvePublish = (%q, %q), want (%q, %q)", owner, invite, tc.wantOwner, tc.wantInv)
			}
		})
	}
}

// TestAResumedPaperReturnsToItsRepository checks the behavior
// docs/local-runs.md documents: "Resume skips what finished and retries what
// failed, into the repository the first attempt created." Until this test
// existed the runner allocated a fresh repository on every retry and overwrote
// the repository in the state file with the new one, so every --resume of a
// publishing project left a sibling repository per attempt.
func TestAResumedPaperReturnsToItsRepository(t *testing.T) {
	const ref = "10.1145/3654777.3676417"
	repos := &fakeRepositories{forge: fakeForge(t)}
	runner := testProjectRunner(t, repos)
	statePath := filepath.Join(t.TempDir(), "batch.json")
	file := batch.File{Papers: []batch.Entry{{Paper: ref}}}

	// The first attempt allocates.
	state, err := batch.Run(t.Context(), file, runner, batch.Options{Parallel: 1, StatePath: statePath, Publishing: true})
	if err != nil {
		t.Fatalf("first attempt: %v: %s", err, state.Entries[0].Error)
	}
	if len(repos.allocated) != 1 || len(repos.reattached) != 0 {
		t.Fatalf("first attempt allocated %v, reattached %v", repos.allocated, repos.reattached)
	}
	first := state.Entries[0].Repo
	if first == "" {
		t.Fatalf("the first attempt recorded no repository: %+v", state.Entries[0])
	}

	// The attempt is then interrupted, which the state records as a failure
	// that still names the repository, and the operator resumes.
	state.Entries[0].Phase, state.Entries[0].Error = batch.Failed, "interrupted"
	if err := state.Save(statePath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	resumed, err := batch.Run(t.Context(), file, runner, batch.Options{Parallel: 1, StatePath: statePath, Resume: true, Publishing: true})
	if err != nil {
		t.Fatalf("resume: %v: %s", err, resumed.Entries[0].Error)
	}
	if len(repos.allocated) != 1 {
		t.Fatalf("the retry allocated a second repository: %v", repos.allocated)
	}
	if len(repos.reattached) != 1 || repos.reattached[0] != first {
		t.Fatalf("the retry reattached to %v, want %s", repos.reattached, first)
	}
	if got := resumed.Entries[0]; got.Repo != first || got.Phase != batch.Done {
		t.Fatalf("after resuming: %+v, want %s and done", got, first)
	}
}

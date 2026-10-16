// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/pkg/hostsandbox"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
)

// allowlisted is a session whose egress is a list of hosts.
func allowlisted() managed.SessionSpec {
	s := spec(false)
	s.Sandbox.Network = managed.NetworkPolicy{Mode: managed.NetworkAllowlist, AllowedDomains: []string{"arxiv.org"}}
	return s
}

// Under an allowlist the model's own endpoint is added, so a session can reach
// the model it was told to run on; the caller who wrote the list does not know
// which endpoint the catalog resolved. The effective list is recorded.
func TestAnAllowlistReachesTheModelsOwnEndpoint(t *testing.T) {
	for name, test := range map[string]struct {
		file    local.CatalogFile
		harness harness.Harness
		want    string
	}{
		"the vendor's endpoint": {local.DefaultCatalogFile(), harness.NewClaudeCode(), "api.anthropic.com"},
		"a gateway's endpoint": {local.CatalogFile{
			Gateways: map[string]local.GatewayConfig{"proxy": {
				BaseURL: "https://llm.example.org/v1", Dialect: string(harness.DialectAnthropic), Auth: string(harness.AuthHarness),
			}},
			Models: []local.ModelConfig{{Provider: "anthropic", ID: "claude-opus-5", Gateway: "proxy",
				Harness: string(harness.ClaudeCode), Preferences: []string{"best"}}},
		}, harness.NewClaudeCode(), "llm.example.org"},
		"codex's vendor": {local.CatalogFile{
			Gateways: map[string]local.GatewayConfig{"login": {Dialect: string(harness.DialectOpenAIResponses), Auth: string(harness.AuthHarness)}},
			Models: []local.ModelConfig{{Provider: "vendor", ID: "gpt-5-codex", Gateway: "login",
				Harness: string(harness.Codex), Preferences: []string{"best"}}},
		}, harness.NewCodex(), "chatgpt.com"},
	} {
		t.Run(name, func(t *testing.T) {
			driver := localtest.New(localtest.Script{})
			registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			catalog, err := local.NewCatalog(test.file, registry, noCredentials)
			if err != nil {
				t.Fatalf("NewCatalog: %v", err)
			}
			runtime, err := local.New(local.Config{Sandbox: driver, Harness: test.harness, Catalog: catalog, Root: t.TempDir(), Lookup: os.LookupEnv})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ref := start(t, runtime, allowlisted())
			runToTerminal(t, runtime, ref)
			worker := stageNamed(t, driver, "worker")
			if worker.Network.Mode != hostsandbox.NetworkAllowlist || !slices.Contains(worker.Network.Domains, test.want) ||
				!slices.Contains(worker.Network.Domains, "arxiv.org") {
				t.Fatalf("the worker may reach %+v, want arxiv.org and %s", worker.Network, test.want)
			}
			if allowed := readEnvironment(t, ref).Network.Allowed; !slices.Contains(allowed, test.want) {
				t.Errorf("the record lists %v, without %s", allowed, test.want)
			}
		})
	}
}

// versionedDriver reports the harness's version, as a real driver does by
// running the harness once, or fails to.
type versionedDriver struct {
	*localtest.Sandbox
	version string
	err     error
}

func (d versionedDriver) Version(context.Context, []string) (string, error) { return d.version, d.err }

// The harness version is recorded where the driver can read it, and left empty
// where it cannot, without failing the run.
func TestTheHarnessVersionIsRecordedWhereItCanBeRead(t *testing.T) {
	for _, test := range []struct {
		driver versionedDriver
		want   string
	}{
		{versionedDriver{Sandbox: localtest.New(localtest.Script{}), version: "2.1.277"}, "2.1.277"},
		{versionedDriver{Sandbox: localtest.New(localtest.Script{}), err: errors.New("not installed")}, ""},
	} {
		registry, _ := harness.NewRegistry(harness.NewClaudeCode())
		catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
		runtime, err := local.New(local.Config{Sandbox: test.driver, Harness: harness.NewClaudeCode(), Catalog: catalog, Root: t.TempDir(), Lookup: os.LookupEnv})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ref := start(t, runtime, spec(false))
		runToTerminal(t, runtime, ref)
		if got := readEnvironment(t, ref).Harness.Version; got != test.want {
			t.Errorf("harness version = %q, want %q", got, test.want)
		}
	}
}

// The packages the bootstrap installed are added to the run's record, read
// from the manifest the bootstrap leaves in the session's environment.
func TestThePackagesTheBootstrapInstalledAreRecorded(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 1})
	ref := start(t, runtime, spec(false))
	manifest := "pypdf==6.1.0\nrequests==2.33.0\n\nnot a pin\n"
	if err := os.WriteFile(filepath.Join(sessionDir(t, ref), "venv", local.PackageManifest), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
	runToTerminal(t, runtime, ref)
	packages := readEnvironment(t, ref).Packages
	if len(packages) != 2 || packages[0].Name != "pypdf" || packages[0].Version != "6.1.0" || packages[1].Name != "requests" {
		t.Fatalf("packages = %+v", packages)
	}
}

// The runtime answers what it is and what it offers, and a file the run
// produced is read back by the artifact the listing names.
func TestTheRuntimeNamesItselfItsModelsAndItsArtifacts(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	if runtime.Name() != local.Name {
		t.Errorf("name = %q", runtime.Name())
	}
	models, err := runtime.Models(context.Background())
	if err != nil || len(models) == 0 {
		t.Fatalf("models = %+v, %v", models, err)
	}
	ref := start(t, runtime, spec(false))
	runToTerminal(t, runtime, ref)
	artifacts, err := runtime.ListArtifacts(context.Background(), ref)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, artifact := range artifacts {
		if artifact.Path != "REPRODUCIBILITY.md" {
			continue
		}
		body, err := runtime.FetchArtifact(context.Background(), ref, artifact)
		if err != nil || !strings.Contains(string(body), "Written by worker") {
			t.Fatalf("FetchArtifact = %q, %v", body, err)
		}
		return
	}
	t.Fatalf("the reproduction is not among the artifacts: %+v", artifacts)
}

// A findings file the runtime cannot read, as opposed to one that is missing,
// fails the poll rather than being recorded as absent, and the next poll can
// try again.
func TestAFindingsFileThatCannotBeReadFailsThePoll(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 1})
	ref := start(t, runtime, checkedSpec(0))
	if err := os.Mkdir(filepath.Join(sessionDir(t, ref), "workspace", "verdict.json"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for range 10 {
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil {
			if !strings.Contains(err.Error(), "verdict.json") {
				t.Fatalf("Poll = %v, want the findings file named", err)
			}
			return
		}
		ref = status.Ref
	}
	t.Fatal("a findings file that cannot be read was never reported")
}

// A configuration that could not run a session is refused when the runtime is
// composed, naming what is missing, rather than at the first stage.
func TestARuntimeThatCouldNotRunIsRefusedWhenComposed(t *testing.T) {
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
	whole := local.Config{Sandbox: localtest.New(localtest.Script{}), Harness: harness.NewClaudeCode(), Catalog: catalog, Root: t.TempDir()}
	for missing, alter := range map[string]func(*local.Config){
		"sandbox": func(c *local.Config) { c.Sandbox = nil },
		"harness": func(c *local.Config) { c.Harness = nil },
		"catalog": func(c *local.Config) { c.Catalog = nil },
		"root":    func(c *local.Config) { c.Root = " " },
	} {
		config := whole
		alter(&config)
		if _, err := local.New(config); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("without a %s: %v", missing, err)
		}
	}
	notReady := whole
	notReady.Sandbox = localtest.New(localtest.Script{NotReady: errors.New("srt is not installed")})
	runtime, err := local.New(notReady)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runtime.Capabilities(context.Background()); err == nil {
		t.Error("a driver that is not ready reported what it can do")
	}
}

// A reference this runtime cannot resolve is refused by every call that takes
// one: one another runtime issued, one whose locator is not this runtime's,
// and one whose session directory is gone or holds no journal. Release alone
// treats a session that is not found as already released.
func TestAReferenceThisRuntimeCannotResolveIsRefused(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	ref := start(t, runtime, spec(false))
	dir := sessionDir(t, ref)

	other := ref
	other.Runtime = "memory"
	garbled := ref
	garbled.Locator = []byte(`{"dir":""}`)
	gone := ref
	gone.Locator = []byte(`{"version":1,"dir":"` + filepath.Join(t.TempDir(), "gone") + `"}`)
	unjournalled := ref
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	unjournalled.Locator = []byte(`{"version":1,"dir":"` + empty + `"}`)

	ctx := context.Background()
	for name, bad := range map[string]managed.SessionRef{"another runtime's": other, "a garbled": garbled, "a gone": gone, "an unjournalled": unjournalled} {
		calls := map[string]error{}
		_, calls["Poll"] = runtime.Poll(ctx, bad)
		_, calls["Events"] = runtime.Events(ctx, bad)
		_, calls["ListArtifacts"] = runtime.ListArtifacts(ctx, bad)
		_, calls["FetchArtifact"] = runtime.FetchArtifact(ctx, bad, managed.Artifact{Path: "README.md"})
		calls["Cancel"] = runtime.Cancel(ctx, bad)
		_, calls["Workspace"] = runtime.Workspace(bad)
		for call, err := range calls {
			if err == nil {
				t.Errorf("%s with %s reference succeeded", call, name)
			}
		}
	}
	if err := runtime.Release(ctx, gone); err != nil {
		t.Errorf("releasing a session that is gone = %v, want nothing to do", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the refusals touched the real session: %v", err)
	}
}

// A session needs an id, and a directory that is not one cannot be opened as
// a session.
func TestASessionNeedsAnIDAndADirectory(t *testing.T) {
	if _, err := local.CreateSession(t.TempDir(), " ", noCredentials); err == nil {
		t.Error("a session with no id was created")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := local.OpenSession(file); !errors.Is(err, managed.ErrNotFound) {
		t.Errorf("a file opened as a session = %v", err)
	}
}

// The bootstrap installs exactly what the specification declared, pinned where
// it pins a version, with each manager's own spelling, and runs the declared
// commands after them.
func TestTheBootstrapInstallsWhatWasDeclared(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	s := spec(false)
	s.Sandbox.Packages = []managed.Package{
		{Manager: "pip", Name: "pypdf", Version: "6.1.0"},
		{Manager: "npm", Name: "left-pad", Version: "1.3.0"},
		{Manager: "npm", Name: "chalk"},
	}
	s.Sandbox.Bootstrap = [][]string{{"python", "-m", "pip", "check"}}
	runToTerminal(t, runtime, start(t, runtime, s))
	script := strings.Join(stageNamed(t, driver, "bootstrap").Argv, " ")
	for _, want := range []string{"pypdf==6.1.0", "left-pad@1.3.0", "chalk", "npm install", "'python' '-m' 'pip' 'check'"} {
		if !strings.Contains(script, want) {
			t.Errorf("the bootstrap does not %q:\n%s", want, script)
		}
	}
}

// A judge's file that names no grade this build knows, or grades no criterion,
// is not a verdict.
func TestAJudgeFileIsAVerdictOnlyWithAGradeAndItsCriteria(t *testing.T) {
	if _, err := (local.JudgeVerdict{Grade: "excellent", Criteria: []local.JudgeCriterion{{ID: 1}}}).Verdict(); err == nil {
		t.Error("an unknown grade was accepted")
	}
	if _, err := (local.JudgeVerdict{Grade: "passed"}).Verdict(); err == nil {
		t.Error("a verdict with no criteria was accepted")
	}
}

// loginless is a sandbox that cannot reach anything of the operator's machine,
// as a container cannot.
type loginless struct{ *localtest.Sandbox }

func (loginless) CarriesHarnessLogin() bool { return false }

// A session on the harness's own login is refused by a sandbox that cannot
// reach that login, before anything starts, and told what to declare instead.
func TestASandboxThatCannotReachTheLoginRefusesASessionOnIt(t *testing.T) {
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
	runtime, err := local.New(local.Config{
		Sandbox: loginless{localtest.New(localtest.Script{})}, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runtime.Validate(spec(false)); !errors.Is(err, managed.ErrUnsupported) || !strings.Contains(err.Error(), "key_env") {
		t.Fatalf("Validate = %v, want a refusal that names key_env", err)
	}
}

// failingDriver fails to stop or to discard a stage, as an engine that has
// gone away does.
type failingDriver struct{ *localtest.Sandbox }

func (failingDriver) Stop(context.Context, hostsandbox.StageHandle) error {
	return errors.New("the engine is not running")
}

func (failingDriver) Discard(context.Context, hostsandbox.StageHandle) error {
	return errors.New("the engine is not running")
}

// A stage the driver cannot stop or discard is reported, not taken as done.
func TestAStageTheDriverCannotEndIsReported(t *testing.T) {
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
	runtime, err := local.New(local.Config{
		Sandbox: failingDriver{localtest.New(localtest.Script{PollsBeforeExit: 100})}, Harness: harness.NewClaudeCode(),
		Catalog: catalog, Root: t.TempDir(), Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref := start(t, runtime, spec(false))
	if err := runtime.Cancel(context.Background(), ref); err == nil {
		t.Error("a stage the driver could not stop was reported cancelled")
	}
	if err := runtime.Release(context.Background(), ref); err == nil {
		t.Error("a stage the driver could not discard was reported released")
	}
}

// A session whose journal has lost a file it needs, or names a stage this
// build does not know, is refused rather than advanced on a guess.
func TestACorruptSessionIsRefused(t *testing.T) {
	for name, corrupt := range map[string]func(t *testing.T, dir string){
		"no status": func(t *testing.T, dir string) { remove(t, filepath.Join(dir, "session", "status.json")) },
		"no spec":   func(t *testing.T, dir string) { remove(t, filepath.Join(dir, "session", "spec.json")) },
		"an unknown stage": func(t *testing.T, dir string) {
			path := filepath.Join(dir, "session", "locator.json")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(body), `"bootstrap"`, `"interlude"`)), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			runtime, _ := build(t, localtest.Script{})
			ref := start(t, runtime, spec(false))
			corrupt(t, sessionDir(t, ref))
			if _, err := runtime.Poll(context.Background(), ref); err == nil {
				t.Fatal("a corrupt session was advanced")
			}
			if name == "no status" {
				if err := runtime.Cancel(context.Background(), ref); err == nil {
					t.Error("a session with no status was cancelled")
				}
			}
		})
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

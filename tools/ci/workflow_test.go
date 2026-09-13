// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// A private repository must route every job, including reusable-workflow
// jobs, to the org runner. Hosted jobs stop before execution at the budget.
func TestVerificationRunsWithoutHostedMinutes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	for _, name := range []string{"ci.yml", "docker.yml"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../.github/workflows", name))
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				Jobs map[string]struct {
					RunsOn string            `yaml:"runs-on"`
					Uses   string            `yaml:"uses"`
					With   map[string]string `yaml:"with"`
					Steps  []struct {
						Uses string         `yaml:"uses"`
						With map[string]any `yaml:"with"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(body, &workflow); err != nil {
				t.Fatal(err)
			}
			if len(workflow.Jobs) == 0 {
				t.Fatal("workflow has no jobs")
			}
			for id, job := range workflow.Jobs {
				if job.Uses != "" {
					if job.With["runs_on"] != "linux-vm" {
						t.Errorf("%s does not route reusable jobs to linux-vm", id)
					}
					var runners []string
					if err := json.Unmarshal([]byte(job.With["test_os"]), &runners); err != nil || len(runners) != 1 || runners[0] != "linux-vm" {
						t.Errorf("%s leaves the test matrix on hosted runners", id)
					}
					continue
				}
				if job.RunsOn != "linux-vm" {
					t.Errorf("%s runs on %q, want linux-vm", id, job.RunsOn)
				}
				node := false
				for _, step := range job.Steps {
					if strings.HasPrefix(step.Uses, "actions/setup-node@") {
						node = true
					}
					if strings.HasPrefix(step.Uses, "oven-sh/setup-bun@") && !node {
						t.Errorf("%s needs real Node before Bun on the bare runner", id)
					}
					if strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["cache"] != false {
						t.Errorf("%s must not restore a hosted Go cache over the persistent cache", id)
					}
				}
			}
		})
	}
}

func TestBrowserJobConfiguresLinuxSandbox(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../.github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	var setup string
	for _, step := range workflow.Jobs["frontend"].Steps {
		if step.Name == "Configure Chrome sandbox" {
			setup = step.Run
		}
		if strings.Contains(step.Run, "bun run test:browser") && setup == "" {
			t.Fatal("browser tests start without configuring the Linux sandbox")
		}
	}
	if setup == "" {
		t.Fatal("missing Chrome sandbox setup")
	}
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "helper available", true: "helper missing"}[missing], func(t *testing.T) {
			dir := t.TempDir()
			helper := filepath.Join(dir, "chrome_sandbox")
			if !missing {
				if err := os.WriteFile(helper, nil, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// Record privileged installer operations without requiring root in
			// the regression test. The browser CI job exercises the real helper.
			if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SANDBOX_SETUP_CALLS\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-c", setup)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PUPPETEER_EXECUTABLE_PATH="+filepath.Join(dir, "chrome"), "GITHUB_ENV="+filepath.Join(dir, "env"), "SANDBOX_SETUP_CALLS="+filepath.Join(dir, "calls"))
			out, err := cmd.CombinedOutput()
			if missing {
				if err == nil {
					t.Fatal("missing sandbox helper must stop the browser job")
				}
				return
			}
			if err != nil {
				t.Fatalf("sandbox setup: %v\n%s", err, out)
			}
			env, err := os.ReadFile(filepath.Join(dir, "env"))
			if err != nil || strings.TrimSpace(string(env)) != "CHROME_DEVEL_SANDBOX="+helper {
				t.Fatalf("browser does not receive its sandbox helper: %s (%v)", env, err)
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil || string(calls) != "chown root:root "+helper+"\nchmod 4755 "+helper+"\n" {
				t.Fatalf("sandbox must be owned by root with mode 4755: %s (%v)", calls, err)
			}
		})
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// runnerPolicy is the runs-on expression of the organization's runner policy
// (latere-ai/ci, README, Runners): for a private repository the self-hosted
// runner, or a hosted one while it is offline.
const runnerPolicy = `${{ (github.event.repository.private || contains(fromJSON('["push","schedule","workflow_dispatch","release"]'), github.event_name)) && (vars.CI_RUNNER_PRIVATE || vars.CI_RUNNER_PUBLIC) || 'ubuntu-latest' }}`

// Every job runs by the runner policy, including reusable-workflow jobs, whose
// runs_on defaults to it. A label pinned in its place would give up the
// fallback: a job aimed at an offline runner waits in the queue for a day.
func TestJobsRunByTheRunnerPolicy(t *testing.T) {
	// Relative to this package's directory, where go test runs it. A path from
	// runtime.Caller is module-relative when the test is built with -trimpath.
	for _, name := range []string{"ci.yml", "docker.yml"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("../../.github/workflows", name))
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
					for _, input := range []string{"runs_on", "test_os"} {
						if _, ok := job.With[input]; ok {
							t.Errorf("%s passes %s, which overrides the runner policy", id, input)
						}
					}
					continue
				}
				if job.RunsOn != runnerPolicy {
					t.Errorf("%s runs on %q, want the runner policy", id, job.RunsOn)
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
	body, err := os.ReadFile("../../.github/workflows/ci.yml")
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

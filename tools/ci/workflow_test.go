// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"os"
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
	for _, name := range []string{"ci.yml"} {
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

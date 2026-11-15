// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/goccy/go-yaml"
)

type probePath struct {
	HTTPGet struct {
		Path string `yaml:"path"`
	} `yaml:"httpGet"`
}

// TestReadinessChecksTheDatabaseAndLivenessDoesNot checks that a pod whose
// database stopped answering leaves the Service (readiness) without being
// restarted (liveness).
func TestReadinessChecksTheDatabaseAndLivenessDoesNot(t *testing.T) {
	f, err := os.Open("base/deployment.yaml")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	checked := 0
	for {
		var d struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name           string    `yaml:"name"`
							LivenessProbe  probePath `yaml:"livenessProbe"`
							ReadinessProbe probePath `yaml:"readinessProbe"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if d.Kind != "Deployment" {
			continue
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			checked++
			if got := c.ReadinessProbe.HTTPGet.Path; got != "/readyz" {
				t.Errorf("%s: readiness probes %q, want /readyz", c.Name, got)
			}
			if got := c.LivenessProbe.HTTPGet.Path; got != "/healthz" {
				t.Errorf("%s: liveness probes %q, want /healthz", c.Name, got)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no container checked")
	}
}

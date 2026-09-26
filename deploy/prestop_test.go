// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
)

// podStop is the part of a Deployment that decides how its pods stop.
type podStop struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				TerminationGracePeriodSeconds *int `yaml:"terminationGracePeriodSeconds"`
				Containers                    []struct {
					Name      string `yaml:"name"`
					Lifecycle struct {
						PreStop struct {
							Sleep struct {
								Seconds int `yaml:"seconds"`
							} `yaml:"sleep"`
						} `yaml:"preStop"`
					} `yaml:"lifecycle"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

// serverDrain is the longest the server takes to finish in-flight requests
// once it is told to stop.
const serverDrain = 10

// A stopping pod must keep serving until the ingress and the Service endpoints
// have dropped its address. The server closes its listener as soon as the stop
// signal arrives; without a pause before that signal, requests still routed to
// the old address got 502 for about a second on every rollout. The pause and
// the drain must both fit in the grace period, or the kubelet kills the pod
// mid-drain.
func TestStoppingPodsServeUntilTrafficMovesAway(t *testing.T) {
	for _, path := range []string{"base/deployment.yaml"} {
		f, err := os.Open(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		dec := yaml.NewDecoder(f)
		deployments := 0
		for {
			var d podStop
			err := dec.Decode(&d)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if d.Kind != "Deployment" {
				continue
			}
			deployments++
			grace := 30 // Kubernetes' default terminationGracePeriodSeconds
			if g := d.Spec.Template.Spec.TerminationGracePeriodSeconds; g != nil {
				grace = *g
			}
			for _, c := range d.Spec.Template.Spec.Containers {
				pause := c.Lifecycle.PreStop.Sleep.Seconds
				if pause < 3 {
					t.Errorf("%s/%s: preStop sleep %ds, want at least 3s so traffic moves away first", d.Metadata.Name, c.Name, pause)
				}
				if pause+serverDrain > grace {
					t.Errorf("%s/%s: preStop %ds + %ds drain exceeds the %ds grace period", d.Metadata.Name, c.Name, pause, serverDrain, grace)
				}
			}
		}
		if err := f.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
		if deployments == 0 {
			t.Errorf("%s holds no Deployment", path)
		}
	}
}

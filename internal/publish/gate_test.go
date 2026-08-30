// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"strings"
	"testing"
)

func TestClassifyIncludesOnlyWorkerOrigin(t *testing.T) {
	if include, _ := Classify(Provenance{Path: "src/main.py", Origin: OriginWorker}); !include {
		t.Error("worker-written artifact should be included")
	}
	include, reason := Classify(Provenance{Path: "data/benchmark.csv", Origin: OriginThirdParty})
	if include {
		t.Error("third-party artifact must not be included")
	}
	if reason == "" {
		t.Error("a withheld artifact needs a reason for EXCLUDED.md")
	}
	// The zero value is not worker-written, so it is never mistaken for one.
	if include, _ := Classify(Provenance{Path: "x", Origin: OriginUnknown}); include {
		t.Error("an unknown origin must fail closed")
	}
}

func TestOriginOfClassifiesStructurally(t *testing.T) {
	worker := []string{"README.md", "REPRODUCIBILITY.md", "UNVERIFIED.md", "SOURCES.md",
		"instrument.json", "src/main.py", "src/lib/util.py", "./src/model.py"}
	for _, p := range worker {
		if originOf(p) != OriginWorker {
			t.Errorf("originOf(%q) = third-party, want worker", p)
		}
	}
	thirdParty := []string{"data/benchmark.csv", "paper.pdf", "downloads/corpus.zip",
		"notes.txt", "srcfoo/x.py"}
	for _, p := range thirdParty {
		if originOf(p) != OriginThirdParty {
			t.Errorf("originOf(%q) = worker, want third-party", p)
		}
	}
}

// AC9: instrument.json survives a publish on both paths — classified
// worker-written by the offline allowlist, and not flagged by the mount-path
// verifier. A worker-written file the gate does not know is withheld silently.
func TestInstrumentJSONSurvivesPublish(t *testing.T) {
	if got := originOf("instrument.json"); got != OriginWorker {
		t.Errorf("originOf(instrument.json) = %v, want OriginWorker", got)
	}
	if include, _ := Classify(Provenance{Path: "instrument.json", Origin: originOf("instrument.json")}); !include {
		t.Error("Classify withheld instrument.json from the filesystem publish path")
	}
	if flagged := VerifyTree([]string{"README.md", "instrument.json", "src/main.py"}); len(flagged) != 0 {
		t.Errorf("VerifyTree flagged instrument.json on the mount path: %v", flagged)
	}
}

// A synthetic pilot's artifacts are never worker-written reproduction files, so
// the allowlist must withhold them from any publication into a reproduction
// repository (007c AC14). They live in the deployment's artifact store and
// never enter a mount, but the gate is the backstop if one ever appeared.
func TestSyntheticArtifactsAreNotPublishable(t *testing.T) {
	for _, p := range []string{"PILOT.md", "cohort.csv", "pilot/PILOT.md"} {
		if originOf(p) == OriginWorker {
			t.Errorf("originOf(%q) = worker — a synthetic artifact must not be publishable", p)
		}
		if include, _ := Classify(Provenance{Path: p, Origin: originOf(p)}); include {
			t.Errorf("Classify admitted %q — a synthetic artifact must be withheld", p)
		}
	}
}

func TestIsSecretName(t *testing.T) {
	secret := []string{".env", ".env.local", "id_rsa", "server.pem", "app.key",
		"deploy/.netrc", "certs/cert.p12"}
	for _, p := range secret {
		if !isSecretName(p) {
			t.Errorf("isSecretName(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"src/main.py", "README.md", "keychain.md"} {
		if isSecretName(p) {
			t.Errorf("isSecretName(%q) = true, want false", p)
		}
	}
}

func TestSuffixShape(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		s, err := NewSuffix()
		if err != nil {
			t.Fatalf("NewSuffix: %v", err)
		}
		if len(s) != suffixLen {
			t.Fatalf("suffix %q length %d, want %d", s, len(s), suffixLen)
		}
		for _, r := range s {
			if !strings.ContainsRune(suffixAlphabet, r) {
				t.Errorf("suffix %q has out-of-alphabet rune %q", s, r)
			}
		}
		seen[s] = true
	}
	if len(seen) < 40 {
		t.Errorf("suffixes barely varied across 50 draws: %d distinct", len(seen))
	}
}

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
		"src/main.py", "src/lib/util.py", "./src/model.py"}
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
	for i := 0; i < 50; i++ {
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

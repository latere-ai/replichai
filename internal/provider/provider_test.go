package provider

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRunStateTerminal(t *testing.T) {
	cases := map[RunState]bool{
		StateUnknown: false,
		StatePending: false,
		StateRunning: false,
		StateDone:    true,
		StateFailed:  true,
	}
	for state, want := range cases {
		if got := state.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", state, got, want)
		}
	}
}

func TestZeroValuesAreNotMeaningful(t *testing.T) {
	// A struct field that was never set must not masquerade as a real state
	// or as a passing grade.
	var state RunState
	if state != StateUnknown || state.Valid() || state.Terminal() {
		t.Errorf("zero RunState must be an invalid, non-terminal unknown, got %s", state)
	}

	var grade Grade
	if grade != GradeUnknown || grade.Decided() {
		t.Errorf("zero Grade must be an undecided unknown, got %s", grade)
	}
	if (Verdict{}).Passed() {
		t.Error("a zero Verdict must not report as passed")
	}
	if (RunStatus{}).Passed() {
		t.Error("a zero RunStatus must not report as passed")
	}
}

func TestGradeDecided(t *testing.T) {
	cases := map[Grade]bool{
		GradeUnknown:       false,
		GradePending:       false,
		GradePassed:        true,
		GradeNeedsRevision: true,
		GradeExhausted:     true,
		GradeFailed:        true,
	}
	for grade, want := range cases {
		if got := grade.Decided(); got != want {
			t.Errorf("%s.Decided() = %v, want %v", grade, got, want)
		}
	}
}

func TestEnumStringsAreStable(t *testing.T) {
	// These strings cross process boundaries (JSON, logs, CLI output), so a
	// rename is a breaking change and should fail here first.
	if got := StateRunning.String(); got != "running" {
		t.Errorf("StateRunning = %q, want running", got)
	}
	if got := GradeNeedsRevision.String(); got != "needs_revision" {
		t.Errorf("GradeNeedsRevision = %q, want needs_revision", got)
	}
	if got := NetworkLimited.String(); got != "limited" {
		t.Errorf("NetworkLimited = %q, want limited", got)
	}
	if got := RunState(200).String(); got != "RunState(200)" {
		t.Errorf("out-of-range state = %q, want RunState(200)", got)
	}
	if got := Grade(200).String(); got != "Grade(200)" {
		t.Errorf("out-of-range grade = %q, want Grade(200)", got)
	}
	if got := Network(200).String(); got != "Network(200)" {
		t.Errorf("out-of-range network = %q, want Network(200)", got)
	}
}

func TestEnumsRoundTripThroughJSON(t *testing.T) {
	type payload struct {
		State   RunState `json:"state"`
		Grade   Grade    `json:"grade"`
		Network Network  `json:"network"`
	}
	original := payload{StateRunning, GradePassed, NetworkLimited}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"state":"running","grade":"passed","network":"limited"}` {
		t.Fatalf("encoded as %s", encoded)
	}

	var decoded payload
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("round trip changed the value: %+v -> %+v", original, decoded)
	}
}

func TestUnmarshalRejectsUnknownNames(t *testing.T) {
	// A value cannot be conjured from an arbitrary string.
	var state RunState
	if err := json.Unmarshal([]byte(`"nonsense"`), &state); err == nil {
		t.Error("unknown run state was accepted")
	}
	var grade Grade
	if err := json.Unmarshal([]byte(`"nonsense"`), &grade); err == nil {
		t.Error("unknown grade was accepted")
	}
	var network Network
	if err := json.Unmarshal([]byte(`"nonsense"`), &network); err == nil {
		t.Error("unknown network policy was accepted")
	}
	if err := json.Unmarshal([]byte(`42`), &state); err == nil {
		t.Error("a non-string run state was accepted")
	}
}

func TestNetworkZeroValueIsOpen(t *testing.T) {
	// An audit's first phase is hunting artifacts across the public web, so
	// open is the working default and restricting is the deliberate act.
	var network Network
	if network != NetworkOpen {
		t.Errorf("zero Network = %s, want open", network)
	}
}

func TestArtifactRecognisesCompiledResidue(t *testing.T) {
	compiled := []string{
		"a/__pycache__/x.cpython-311.pyc",
		"x.pyc",
		"x.pyo",
	}
	for _, path := range compiled {
		if !(Artifact{Path: path}).IsCompiled() {
			t.Errorf("%q should be recognised as compiled residue", path)
		}
	}
	for _, path := range []string{"x.py", "README.md", "src/core.go"} {
		if (Artifact{Path: path}).IsCompiled() {
			t.Errorf("%q should not be treated as compiled residue", path)
		}
	}
}

func TestRegistry(t *testing.T) {
	t.Run("get rejects an unknown name", func(t *testing.T) {
		if _, err := Get("nope"); !errors.Is(err, ErrUnknownProvider) {
			t.Fatalf("want ErrUnknownProvider, got %v", err)
		}
	})

	t.Run("register and retrieve", func(t *testing.T) {
		Register("stub-for-test", func() (Provider, error) { return nil, nil })
		if _, err := Get("STUB-FOR-TEST"); err != nil {
			t.Errorf("lookup should be case-insensitive: %v", err)
		}
		if _, err := Get("  stub-for-test  "); err != nil {
			t.Errorf("lookup should trim whitespace: %v", err)
		}
		var found bool
		for _, name := range Available() {
			if name == "stub-for-test" {
				found = true
			}
		}
		if !found {
			t.Errorf("registered provider missing from Available(): %v", Available())
		}
	})

	t.Run("available is sorted", func(t *testing.T) {
		names := Available()
		for i := 1; i < len(names); i++ {
			if names[i-1] > names[i] {
				t.Fatalf("Available() is not sorted: %v", names)
			}
		}
	})

	t.Run("empty name selects the default", func(t *testing.T) {
		Register(DefaultName, func() (Provider, error) { return nil, errSentinel })
		if _, err := Get(""); !errors.Is(err, errSentinel) {
			t.Errorf("empty name did not resolve to %q: %v", DefaultName, err)
		}
	})
}

var errSentinel = errors.New("sentinel")

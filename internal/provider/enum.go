package provider

import (
	"encoding/json"
	"fmt"
)

// RunState is where a run sits in its lifecycle.
//
// The zero value is StateUnknown, so a struct field that was never set cannot
// masquerade as a meaningful state.
type RunState uint8

const (
	StateUnknown RunState = iota
	StatePending
	StateRunning
	StateDone
	StateFailed
)

var runStateNames = map[RunState]string{
	StateUnknown: "unknown",
	StatePending: "pending",
	StateRunning: "running",
	StateDone:    "done",
	StateFailed:  "failed",
}

// Terminal reports whether no further state change is expected.
func (s RunState) Terminal() bool { return s == StateDone || s == StateFailed }

// Valid reports whether s is a state this package defines.
func (s RunState) Valid() bool { _, ok := runStateNames[s]; return ok && s != StateUnknown }

func (s RunState) String() string {
	if name, ok := runStateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("RunState(%d)", uint8(s))
}

func (s RunState) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *RunState) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for state, n := range runStateNames {
		if n == name {
			*s = state
			return nil
		}
	}
	return fmt.Errorf("unknown run state %q", name)
}

// Grade is the outcome of grading a run against the rubric.
//
// The zero value is GradeUnknown: a run carries no grade until a grader
// records one, and an unset grade must never read as a pass.
type Grade uint8

const (
	GradeUnknown Grade = iota
	GradePending
	GradePassed
	GradeNeedsRevision
	GradeExhausted
	GradeFailed
)

var gradeNames = map[Grade]string{
	GradeUnknown:       "unknown",
	GradePending:       "pending",
	GradePassed:        "passed",
	GradeNeedsRevision: "needs_revision",
	GradeExhausted:     "exhausted",
	GradeFailed:        "failed",
}

// Decided reports whether the grader has reached a final judgement.
func (g Grade) Decided() bool {
	return g == GradePassed || g == GradeNeedsRevision ||
		g == GradeExhausted || g == GradeFailed
}

func (g Grade) String() string {
	if name, ok := gradeNames[g]; ok {
		return name
	}
	return fmt.Sprintf("Grade(%d)", uint8(g))
}

func (g Grade) MarshalJSON() ([]byte, error) { return json.Marshal(g.String()) }

func (g *Grade) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for grade, n := range gradeNames {
		if n == name {
			*g = grade
			return nil
		}
	}
	return fmt.Errorf("unknown grade %q", name)
}

// Network is a sandbox's outbound network policy.
//
// The zero value is NetworkOpen: an audit's whole first phase is hunting for
// artifacts across the public web, so open is the working default and
// restricting is the deliberate act.
type Network uint8

const (
	NetworkOpen Network = iota
	NetworkLimited
)

var networkNames = map[Network]string{
	NetworkOpen:    "open",
	NetworkLimited: "limited",
}

func (n Network) String() string {
	if name, ok := networkNames[n]; ok {
		return name
	}
	return fmt.Sprintf("Network(%d)", uint8(n))
}

func (n Network) MarshalJSON() ([]byte, error) { return json.Marshal(n.String()) }

func (n *Network) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for network, s := range networkNames {
		if s == name {
			*n = network
			return nil
		}
	}
	return fmt.Errorf("unknown network policy %q", name)
}

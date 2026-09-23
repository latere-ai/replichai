// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"encoding/json"
	"fmt"
)

// RunState is where a run sits in its lifecycle.
//
// The zero value is StateUnknown, so an unset field cannot read as a
// meaningful state.
type RunState uint8

// The lifecycle a run moves through.
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

// String returns the value's stable name.
func (s RunState) String() string {
	if name, ok := runStateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("RunState(%d)", uint8(s))
}

// MarshalJSON encodes the value as its stable name.
func (s RunState) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON decodes a stable name, rejecting anything unrecognized.
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

// The grades a run can be given.
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

// Decided reports whether the grader has reached a final judgment.
func (g Grade) Decided() bool {
	return g == GradePassed || g == GradeNeedsRevision ||
		g == GradeExhausted || g == GradeFailed
}

// String returns the value's stable name.
func (g Grade) String() string {
	if name, ok := gradeNames[g]; ok {
		return name
	}
	return fmt.Sprintf("Grade(%d)", uint8(g))
}

// MarshalJSON encodes the value as its stable name.
func (g Grade) MarshalJSON() ([]byte, error) { return json.Marshal(g.String()) }

// UnmarshalJSON decodes a stable name, rejecting anything unrecognized.
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
// The zero value is NetworkOpen: an audit's first phase searches the public
// web for artifacts, so open is the default and limiting is an explicit
// choice.
type Network uint8

// The outbound network policies a sandbox can have.
const (
	NetworkOpen Network = iota
	NetworkLimited
)

var networkNames = map[Network]string{
	NetworkOpen:    "open",
	NetworkLimited: "limited",
}

// String returns the value's stable name.
func (n Network) String() string {
	if name, ok := networkNames[n]; ok {
		return name
	}
	return fmt.Sprintf("Network(%d)", uint8(n))
}

// MarshalJSON encodes the value as its stable name.
func (n Network) MarshalJSON() ([]byte, error) { return json.Marshal(n.String()) }

// UnmarshalJSON decodes a stable name, rejecting anything unrecognized.
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

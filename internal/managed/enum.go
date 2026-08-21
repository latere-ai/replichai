// Package managed defines the provider-neutral contract for long-running,
// sandboxed model sessions.
package managed

import (
	"encoding/json"
	"fmt"
)

// State is the externally visible lifecycle of a managed session.
type State uint8

const (
	StateUnknown State = iota
	StatePending
	StateRunning
	StateDone
	StateFailed
)

var stateNames = map[State]string{
	StateUnknown: "unknown",
	StatePending: "pending",
	StateRunning: "running",
	StateDone:    "done",
	StateFailed:  "failed",
}

func (s State) Terminal() bool { return s == StateDone || s == StateFailed }
func (s State) Valid() bool    { return s > StateUnknown && s <= StateFailed }
func (s State) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("State(%d)", s)
}
func (s State) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }
func (s *State) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return err
	}
	for value, candidate := range stateNames {
		if candidate == name {
			*s = value
			return nil
		}
	}
	return fmt.Errorf("unknown managed state %q", name)
}

// Grade is a runtime evaluator's decision.
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

func (g Grade) Decided() bool {
	return g == GradePassed || g == GradeNeedsRevision || g == GradeExhausted || g == GradeFailed
}
func (g Grade) String() string {
	if name, ok := gradeNames[g]; ok {
		return name
	}
	return fmt.Sprintf("Grade(%d)", g)
}
func (g Grade) MarshalJSON() ([]byte, error) { return json.Marshal(g.String()) }
func (g *Grade) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return err
	}
	for value, candidate := range gradeNames {
		if candidate == name {
			*g = value
			return nil
		}
	}
	return fmt.Errorf("unknown managed grade %q", name)
}

type NetworkMode string

const (
	NetworkNone      NetworkMode = "none"
	NetworkAllowlist NetworkMode = "allowlist"
	NetworkOpen      NetworkMode = "open"
)

type AccessMode string

const (
	AccessReadOnly  AccessMode = "ro"
	AccessReadWrite AccessMode = "rw"
)

type ResourceKind string

const (
	ResourceAttachment ResourceKind = "attachment"
	ResourceRepository ResourceKind = "repository"
)

type TerminalReason string

const (
	ReasonSucceeded        TerminalReason = "succeeded"
	ReasonEvaluationFailed TerminalReason = "evaluation_failed"
	ReasonCancelled        TerminalReason = "cancelled"
	ReasonTimedOut         TerminalReason = "timed_out"
	ReasonRuntimeFailed    TerminalReason = "runtime_failed"
	ReasonPolicyRefused    TerminalReason = "policy_refused"
)

type Role string

const (
	RoleAgent  Role = "agent"
	RoleUser   Role = "user"
	RoleSystem Role = "system"
)

type EventKind string

const (
	KindMessage    EventKind = "message"
	KindThinking   EventKind = "thinking"
	KindToolUse    EventKind = "tool_use"
	KindToolResult EventKind = "tool_result"
)

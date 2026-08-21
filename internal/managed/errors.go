package managed

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidSpec     = errors.New("invalid managed session specification")
	ErrUnsupported     = errors.New("managed session capability unsupported")
	ErrRuntimeMismatch = errors.New("managed session runtime mismatch")
	ErrNotFound        = errors.New("managed session not found")
	ErrContract        = errors.New("managed runtime contract violation")
)

type InvalidSpecError struct {
	Field   string
	Message string
}

func (e *InvalidSpecError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrInvalidSpec, e.Field, e.Message)
}
func (e *InvalidSpecError) Unwrap() error { return ErrInvalidSpec }

type UnsupportedError struct {
	Runtime    RuntimeName
	Field      string
	Capability string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%v: runtime %q does not support %s (%s)", ErrUnsupported, e.Runtime, e.Capability, e.Field)
}
func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

type RuntimeMismatchError struct {
	Expected RuntimeName
	Actual   RuntimeName
}

func (e *RuntimeMismatchError) Error() string {
	return fmt.Sprintf("%v: expected %q, got %q", ErrRuntimeMismatch, e.Expected, e.Actual)
}
func (e *RuntimeMismatchError) Unwrap() error { return ErrRuntimeMismatch }

type NotFoundError struct {
	Runtime RuntimeName
	ID      string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%v: %s/%s", ErrNotFound, e.Runtime, e.ID)
}
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// TemporaryError marks an upstream failure which the controller may retry.
type TemporaryError struct {
	Err   error
	After time.Duration
}

func (e *TemporaryError) Error() string   { return e.Err.Error() }
func (e *TemporaryError) Unwrap() error   { return e.Err }
func (e *TemporaryError) Temporary() bool { return true }
func (e *TemporaryError) RetryAfter() time.Duration {
	return e.After
}

func IsTemporary(err error) bool {
	var temporary interface{ Temporary() bool }
	return errors.As(err, &temporary) && temporary.Temporary()
}

type CleanupError struct{ Err error }

func (e *CleanupError) Error() string { return "managed session cleanup: " + e.Err.Error() }
func (e *CleanupError) Unwrap() error { return e.Err }

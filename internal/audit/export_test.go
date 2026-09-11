// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"time"

	"github.com/latere-ai/replichai/internal/instrument"
)

// Nothing in production uses the options below: the server configures the
// legacy Auditor with its own options and the CLI runs sessions through
// SessionAuditor. The tests that drive the legacy loop quickly still need
// them, so they are defined here, where they are visible to this package's
// internal and external tests and absent from the API a caller sees.

// WithWorkerName overrides the name given to the worker.
func WithWorkerName(name string) Option {
	return func(a *Auditor) { a.workerName = name }
}

// WithMaxRevisions overrides how many times the worker may revise.
func WithMaxRevisions(n int) Option {
	return func(a *Auditor) {
		if n > 0 {
			a.maxRevisions = n
		}
	}
}

// WithPollInterval overrides the gap between status checks.
func WithPollInterval(d time.Duration) Option {
	return func(a *Auditor) { a.pollInterval = d }
}

// WithTimeout overrides how long a run may take before it is abandoned.
func WithTimeout(d time.Duration) Option {
	return func(a *Auditor) { a.timeout = d; a.unbounded = false }
}

// WithPollFailures overrides how many consecutive failed observations are
// tolerated before the wait gives up.
func WithPollFailures(n int) Option {
	return func(a *Auditor) {
		if n > 0 {
			a.pollFailures = n
		}
	}
}

// instrumentOK reports whether a run wrote a schema-valid instrument.json,
// from the two fields a result carries.
func instrumentOK(present bool, violations []instrument.Violation) bool {
	return present && len(violations) == 0
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import "context"

type workerKey struct{}

// WithWorker names the replica making a call as the driver of a session. A
// store then writes to a session only while that replica holds its lease, and
// refuses with ErrLeaseLost when another replica does. The worker sets it on
// every context it drives a session under, so the runner's observations carry
// it too. A call that names no driver is an operator's or a test's, and is
// checked only against the session's phase.
func WithWorker(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, workerKey{}, id)
}

// workerFrom is the replica WithWorker named, or "".
func workerFrom(ctx context.Context) string {
	id, _ := ctx.Value(workerKey{}).(string)
	return id
}

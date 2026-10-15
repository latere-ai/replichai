// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import "context"

type ownerKey struct{}

// WithSessionOwner names the person a call about a session is made for: the
// one who started it and, on a runtime the person pays, the one it acts as
// (048). The worker sets it where it reaches a session by reference alone,
// such as cancelling one, so the runner can act as that person.
func WithSessionOwner(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, ownerKey{}, principal)
}

// SessionOwner is the person WithSessionOwner named, or "".
func SessionOwner(ctx context.Context) string {
	owner, _ := ctx.Value(ownerKey{}).(string)
	return owner
}

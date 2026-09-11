// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import "context"

// WithPrincipal carries a subject-only identity onto a request context. Only
// the tests build requests this way; every real caller arrives through the
// auth middleware, which sets the whole identity.
func WithPrincipal(ctx context.Context, sub string) context.Context {
	return WithIdentity(ctx, Identity{Sub: sub})
}

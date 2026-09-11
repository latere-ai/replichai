// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"sync"
	"time"
)

// memo holds one computed value for a bounded time. It exists for the two
// responses served without signing in, the public registry and the sitemap,
// which are the same for every anonymous caller and cost several queries per
// public project to build. Building them once per TTL, and again as soon as a
// publication changes, means a crawler's request reads a cached slice rather
// than running a table scan.
type memo[T any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	at    time.Time
	value T
	set   bool
	now   func() time.Time
}

// get returns the held value while it is fresh, or fills and holds a new one.
// An error from fill is returned and nothing is held, so a failed build is
// retried on the next call rather than cached.
func (m *memo[T]) get(ctx context.Context, fill func(context.Context) (T, error)) (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now
	if m.now != nil {
		now = m.now
	}
	if m.set && now().Sub(m.at) < m.ttl {
		return m.value, nil
	}
	value, err := fill(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	m.value, m.at, m.set = value, now(), true
	return value, nil
}

// invalidate drops the held value, so the next call rebuilds it. It is called
// when a publication changes, so the public response does not lag behind what
// an owner just did.
func (m *memo[T]) invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set = false
}

// publicTTL bounds how long the anonymous registry and the sitemap are held
// between rebuilds when nothing invalidates them. A rename or a new session
// appears within this time; a publication change appears at once.
const publicTTL = 30 * time.Second

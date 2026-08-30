// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"testing"

	"github.com/latere-ai/replichai/internal/provider"
)

// A cache write is billed above fresh input, so a derived figure that drops it
// under-reports the run by its most expensive input class (011). A span reports
// creation as one number with no lifetime, unlike the session total, so it is
// carried unattributed rather than assigned to a bucket it may not belong to.
func TestEventUsageCarriesCacheCreationUnattributed(t *testing.T) {
	e := provider.Event{Usage: &provider.Usage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10, CacheWriteTokens: 400}}

	got, ok := eventUsage(e)
	if !ok {
		t.Fatal("eventUsage reported no usage for a charged event")
	}
	want := provider.Usage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10, CacheWriteTokens: 400}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if got.CacheWrite5mTokens != 0 || got.CacheWrite1hTokens != 0 {
		t.Error("a span's cache creation was assigned a lifetime the backend never reported")
	}
	if n := got.Total(); n != 560 {
		t.Errorf("total = %d, want 560 — a class missing from Total is a class nobody is charged for", n)
	}
}

// The session total is the sum of exactly the per-event figures the viewer
// shows (006s), and that has to keep holding for a class added later — which is
// why the sum goes through Usage.Add rather than a hand-written field list.
func TestAttachUsageSumsEveryClass(t *testing.T) {
	events := []provider.Event{
		{Usage: &provider.Usage{InputTokens: 10, OutputTokens: 1, CachedInputTokens: 2, CacheWriteTokens: 3}},
		{},
		{Usage: &provider.Usage{InputTokens: 20, OutputTokens: 2, CachedInputTokens: 4, CacheWriteTokens: 6}},
	}

	out, total, ok := attachUsage(events)
	if !ok {
		t.Fatal("attachUsage found no usage")
	}
	want := provider.Usage{InputTokens: 30, OutputTokens: 3, CachedInputTokens: 6, CacheWriteTokens: 9}
	if total != want {
		t.Fatalf("total = %+v, want %+v", total, want)
	}
	var sum int64
	for _, e := range out {
		if e.Usage != nil {
			sum += e.Usage.Total()
		}
	}
	if sum != total.Total() {
		t.Fatalf("per-event sum %d != session total %d", sum, total.Total())
	}
	if out[1].Usage != nil {
		t.Error("an uncharged event carries usage; an absent number is a fact about the run")
	}
}

// Add is the one place a sum over usages lives, so a class added to the struct
// cannot be forgotten by a caller summing fields by hand.
func TestUsageAddCoversEveryClass(t *testing.T) {
	one := provider.Usage{
		InputTokens: 1, OutputTokens: 2, CachedInputTokens: 3,
		CacheWrite5mTokens: 4, CacheWrite1hTokens: 5, CacheWriteTokens: 6,
	}
	var got provider.Usage
	got.Add(one)
	got.Add(one)

	want := provider.Usage{
		InputTokens: 2, OutputTokens: 4, CachedInputTokens: 6,
		CacheWrite5mTokens: 8, CacheWrite1hTokens: 10, CacheWriteTokens: 12,
	}
	if got != want {
		t.Fatalf("summed = %+v, want %+v", got, want)
	}
	if got.Total() != 42 {
		t.Errorf("total = %d, want 42", got.Total())
	}
	if got.CacheWritten() != 30 {
		t.Errorf("cache written = %d, want 30", got.CacheWritten())
	}
}

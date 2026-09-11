// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/httpapi"
)

const testKey = "0000000000000000000000000000000000000000000000000000000000000000" // 32 bytes

func TestCodecRoundTripAndTamper(t *testing.T) {
	c, err := NewCodec(testKey)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	sealed, err := c.Seal(Session{Sub: "u1", Email: "u@x.com"}, time.Hour)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	var got Session
	if err := c.Open(sealed, &got); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Sub != "u1" || got.Email != "u@x.com" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// A tampered value fails authentication rather than yielding a forged one.
	// Flip an interior base64 character, not the last one: the final character
	// carries non-canonical low bits, so flipping it can decode to identical
	// ciphertext and open legitimately. The tamper must change decoded bytes.
	mid := len(sealed) / 2
	tampered := sealed[:mid] + flip(sealed[mid]) + sealed[mid+1:]
	if err := c.Open(tampered, &Session{}); err == nil {
		t.Error("a tampered cookie must not open")
	}

	// A different key cannot open it.
	other, _ := NewCodec("1111111111111111111111111111111111111111111111111111111111111111")
	if err := other.Open(sealed, &Session{}); err == nil {
		t.Error("a wrong-key cookie must not open")
	}

	// Garbage is rejected.
	if err := c.Open("!!!not-base64!!!", &Session{}); err == nil {
		t.Error("non-base64 must be rejected")
	}
	if err := c.Open("AAAA", &Session{}); err == nil {
		t.Error("too-short ciphertext must be rejected")
	}
}

func TestNewCodecRejectsBadKey(t *testing.T) {
	for _, k := range []string{"", "short", "zz00"} {
		if _, err := NewCodec(k); err == nil {
			t.Errorf("key %q should be rejected", k)
		}
	}
}

// AC3: the session carries no tier. Asserted structurally so a future "cache the
// tier for speed" change cannot break revocation-on-next-request.
func TestSessionCarriesNoTier(t *testing.T) {
	ty := reflect.TypeFor[Session]()
	for field := range ty.Fields() {
		name := strings.ToLower(field.Name)
		if strings.Contains(name, "tier") || strings.Contains(name, "role") || strings.Contains(name, "grant") {
			t.Errorf("Session has field %q — authority must be re-resolved, never cached in the session", field.Name)
		}
	}
}

func flip(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

// A sealed value carries its own expiry, judged by the server. A cookie the
// browser was told to keep for twelve hours is refused at hour thirteen
// wherever it was copied to. Before this check, only the browser's MaxAge
// prevented indefinite replay of a leaked cookie.
func TestASealedValueExpiresOnTheServersClock(t *testing.T) {
	c, err := NewCodec(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	sealed, err := c.Seal(Session{Sub: "u1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var s Session
	if err := c.Open(sealed, &s); err != nil || s.Sub != "u1" {
		t.Fatalf("fresh: %v %+v", err, s)
	}
	now = now.Add(59 * time.Minute)
	if err := c.Open(sealed, &s); err != nil {
		t.Fatalf("within the hour: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := c.Open(sealed, &s); !errors.Is(err, ErrExpired) {
		t.Fatalf("past the hour: err=%v, want ErrExpired", err)
	}
}

// An expired session cookie leaves the request unauthenticated, exactly as
// no cookie would; the gate then refuses it.
func TestAnExpiredSessionCookieIsNoSession(t *testing.T) {
	c, _ := NewCodec(strings.Repeat("ab", 32))
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	l := &Login{codec: c}

	sealed, _ := c.Seal(Session{Sub: "u1", Email: "u@x.com"}, sessionTTL)
	now = now.Add(sessionTTL + time.Minute)

	var seen httpapi.Identity
	var ok bool
	h := l.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, ok = httpapi.IdentityFrom(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName(sessionCookie, false), Value: sealed})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if ok && seen.Sub != "" {
		t.Fatalf("an expired cookie authenticated %+v", seen)
	}
}

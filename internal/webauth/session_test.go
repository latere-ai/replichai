package webauth

import (
	"reflect"
	"strings"
	"testing"
)

const testKey = "0000000000000000000000000000000000000000000000000000000000000000" // 32 bytes

func TestCodecRoundTripAndTamper(t *testing.T) {
	c, err := NewCodec(testKey)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	sealed, err := c.Seal(Session{Sub: "u1", Email: "u@x.com"})
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
	// ciphertext and open legitimately — the tamper must actually change bytes.
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
	ty := reflect.TypeOf(Session{})
	for i := 0; i < ty.NumField(); i++ {
		name := strings.ToLower(ty.Field(i).Name)
		if strings.Contains(name, "tier") || strings.Contains(name, "role") || strings.Contains(name, "grant") {
			t.Errorf("Session has field %q — authority must be re-resolved, never cached in the session", ty.Field(i).Name)
		}
	}
}

func flip(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Session is what the login writes into the session cookie: the identity claims
// the gate needs, the display claims the shell renders, and nothing else. It
// deliberately carries NO tier — the gate re-resolves the grant on every
// request, so a revoked grant locks its holder out immediately and no session
// caches authority (006c AC3). A structural test enforces the absence of a tier
// field.
//
// Name and Picture are display data only (006m). They are kept here, in a
// client-held sealed cookie, rather than in a server table: access_grants is an
// ACL, not a user table. They confer nothing — no display claim reaches an
// authorisation decision — and they go stale until the next sign-in, bounded by
// sessionTTL.
type Session struct {
	Sub     string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name,omitempty"`
	Picture string `json:"picture,omitempty"`
}

// Cookie names. The __Host- prefix binds a cookie to Secure + Path=/ + no
// Domain, which the browser enforces — a hardening that costs nothing in prod
// and is dropped only under the insecure-cookies dev escape.
const (
	sessionCookie = "__Host-replichai-session"
	flowCookie    = "__Host-replichai-flow"
)

// Codec seals and opens small values into authenticated, encrypted cookie
// strings with AES-256-GCM. Hand-rolled here on purpose: oidc owns the
// token flow but not session storage, and pkg/oidc's codec is welded to its own
// login Client, so this is the one place the session crypto lives — kept to this
// file and covered by round-trip and tamper tests.
type Codec struct {
	aead cipher.AEAD
	// now is the clock the expiry is judged by; tests move it.
	now func() time.Time
}

// ErrExpired reports a sealed value past the lifetime it was sealed with.
var ErrExpired = errors.New("sealed value has expired")

// envelope is what is actually sealed: the value and the moment it stops
// being valid. The expiry travels inside the ciphertext, so a cookie's MaxAge
// is only a hint to the browser and the server judges age itself — a value
// taken from a browser and replayed later is refused however long it is kept.
type envelope struct {
	Expires int64           `json:"exp"`
	Value   json.RawMessage `json:"v"`
}

// NewCodec builds a codec from a 64-hex-char (32-byte) key, e.g. from
// `openssl rand -hex 32`.
func NewCodec(hexKey string) (*Codec, error) {
	key, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("AUTH_COOKIE_KEY must be 64 hex characters (32 bytes)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Codec{aead: aead, now: time.Now}, nil
}

// Seal encrypts v (JSON) into a URL-safe base64 string: nonce ‖ ciphertext.
func (c *Codec) Seal(v any, ttl time.Duration) (string, error) {
	value, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(envelope{Expires: c.now().Add(ttl).Unix(), Value: value})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plain, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts and authenticates a sealed string into v. A tampered or
// wrong-key value fails authentication and returns an error rather than
// yielding a forged value.
func (c *Codec) Open(s string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return errors.New("sealed value too short")
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(plain, &env); err != nil {
		return err
	}
	if env.Expires == 0 || c.now().Unix() > env.Expires {
		return ErrExpired
	}
	return json.Unmarshal(env.Value, v)
}

// setCookie writes a sealed value as a hardened cookie. secure=false drops the
// Secure flag and the __Host- prefix for local plain-HTTP dev only.
func setCookie(w http.ResponseWriter, name, value string, secure bool, ttl time.Duration) {
	//nolint:gosec // G124: HttpOnly and SameSite are set unconditionally; Secure is the caller's, and cmd/server/main.go derives it from the base URL's scheme with no override, so it is off only for a plain-HTTP local run — where cookieName also drops the __Host- prefix the browser would then reject.
	c := &http.Cookie{
		Name:     cookieName(name, secure),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	}
	http.SetCookie(w, c)
}

// clearCookie expires a cookie.
func clearCookie(w http.ResponseWriter, name string, secure bool) {
	//nolint:gosec // G124: the expiring cookie mirrors setCookie's attributes exactly, Secure included, because a browser only replaces a cookie whose name, path, and security attributes match the one it holds.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(name, secure),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// readCookie returns a cookie's raw value.
func readCookie(r *http.Request, name string, secure bool) (string, bool) {
	c, err := r.Cookie(cookieName(name, secure))
	if err != nil {
		return "", false
	}
	return c.Value, true
}

// cookieName drops the __Host- prefix when Secure is off, because the browser
// refuses a __Host- cookie without Secure.
func cookieName(name string, secure bool) string {
	if secure {
		return name
	}
	return strings.TrimPrefix(name, "__Host-")
}

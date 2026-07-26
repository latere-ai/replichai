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
// the gate needs, and nothing else. It deliberately carries NO tier — the gate
// re-resolves the grant on every request, so a revoked grant locks its holder
// out immediately and no session caches authority (006c AC3). A structural test
// enforces the absence of a tier field.
type Session struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"ev"`
}

// Cookie names. The __Host- prefix binds a cookie to Secure + Path=/ + no
// Domain, which the browser enforces — a hardening that costs nothing in prod
// and is dropped only under the insecure-cookies dev escape.
const (
	sessionCookie = "__Host-replichai-session"
	flowCookie    = "__Host-replichai-flow"
)

// Codec seals and opens small values into authenticated, encrypted cookie
// strings with AES-256-GCM. Hand-rolled here on purpose: oidclogin owns the
// token flow but not session storage, and pkg/oidc's codec is welded to its own
// login Client, so this is the one place the session crypto lives — kept to this
// file and covered by round-trip and tamper tests.
type Codec struct {
	aead cipher.AEAD
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
	return &Codec{aead: aead}, nil
}

// Seal encrypts v (JSON) into a URL-safe base64 string: nonce ‖ ciphertext.
func (c *Codec) Seal(v any) (string, error) {
	plain, err := json.Marshal(v)
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
	return json.Unmarshal(plain, v)
}

// setCookie writes a sealed value as a hardened cookie. secure=false drops the
// Secure flag and the __Host- prefix for local plain-HTTP dev only.
func setCookie(w http.ResponseWriter, name, value string, secure bool, ttl time.Duration) {
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

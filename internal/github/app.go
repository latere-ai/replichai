package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"
)

// appAuth is the GitHub App identity that mints mount tokens (006f's two-token
// split). The server-side operations keep using the org token; only the sandbox
// mount gets a token minted here — short-lived and scoped to a single repo, so a
// leak inside the sandbox costs that one repository, not the organisation.
type appAuth struct {
	appID          string
	installationID string
	key            *rsa.PrivateKey
	now            func() time.Time // overridable in tests
}

// WithApp configures the GitHub App that mints scoped mount tokens: the App's
// numeric app id and installation id, and its RSA private key. A client without
// it cannot mint a mount token and says so, rather than falling back to the org
// token inside the sandbox.
func WithApp(appID, installationID string, key *rsa.PrivateKey) Option {
	return func(c *Client) {
		c.app = &appAuth{appID: appID, installationID: installationID, key: key, now: time.Now}
	}
}

// HasApp reports whether a scoped mount token can be minted.
func (c *Client) HasApp() bool { return c.app != nil }

// ParseAppKey parses a GitHub App's PEM private key (PKCS#1, as GitHub issues it,
// or PKCS#8).
func ParseAppKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in the app key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse app key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("app key is not an RSA key")
	}
	return key, nil
}

// MintMountToken mints a short-lived installation token scoped to exactly the
// one repository named — the credential the sandbox mount carries (006f). It
// never returns the org token; a client with no App configured errors rather
// than handing the server token to the sandbox.
func (c *Client) MintMountToken(ctx context.Context, owner, name string) (string, error) {
	if c.app == nil {
		return "", fmt.Errorf("no GitHub App configured: refusing to mint a mount token (the server token must never enter the sandbox)")
	}
	assertion, err := c.app.jwt()
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{
		// Scope the token to this one repository, write to its contents only —
		// exactly enough for the agent to clone and push, and nothing else.
		"repositories": []string{name},
		"permissions":  map[string]string{"contents": "write"},
	})
	url := c.base + "/app/installations/" + c.app.installationID + "/access_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+assertion) // the App JWT, not the org token
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("mint mount token for %s/%s: status %d", owner, name, resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(payload, &out); err != nil || out.Token == "" {
		return "", fmt.Errorf("mint mount token for %s/%s: no token in response", owner, name)
	}
	return out.Token, nil
}

// jwt builds the short-lived RS256 assertion GitHub requires to act as the App.
// A small backdated iat absorbs clock skew; the exp is well under GitHub's
// 10-minute ceiling.
func (a *appAuth) jwt() (string, error) {
	now := a.now()
	header := b64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := b64url([]byte(fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":%q}`,
		now.Add(-30*time.Second).Unix(), now.Add(9*time.Minute).Unix(), a.appID)))
	signingInput := header + "." + claims
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

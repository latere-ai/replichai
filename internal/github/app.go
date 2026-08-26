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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// This file makes github.Client authenticate as a GitHub App instead of with a
// personal access token. The App is the only credential that can *mint* a
// scoped, short-lived token — which is what lets the mount carry a per-repository
// token instead of the organisation's (009). Every server-side call keeps its
// existing shape: the Client transparently mints, caches, and refreshes the
// App's installation token and sends it as the Bearer, so no caller changes.
//
// An installation token lives at most an hour; the server lives for weeks. So
// the token is never captured once at startup — it is refreshed on expiry inside
// bearer(), the one change that would 401 the whole server an hour after a deploy
// if it were missing.

// appAuth holds the GitHub App's signing key and the derived, cached installation
// token. It is safe for concurrent use: the HTTP handlers and the worker share
// one Client.
type appAuth struct {
	appID   int64
	key     *rsa.PrivateKey
	owner   string // the org the App is installed on; its installation is resolved lazily
	base    string
	http    *http.Client
	now     func() time.Time
	refresh time.Duration // re-mint this long before the token actually expires

	mu       sync.Mutex
	instID   int64     // resolved installation id (0 until first resolved)
	token    string    // cached broad installation token
	tokenExp time.Time // its expiry
}

// NewApp builds a Client that authenticates as a GitHub App installed on owner.
// privateKeyPEM is the App's private key (the .pem GitHub generates, PKCS#1 or
// PKCS#8). It replaces New for the server; all of CreateRepo/Invite/HeadCommit/
// TreePaths/PutFile/SetPublic/ListRepos then run on the installation token.
func NewApp(appID int64, privateKeyPEM []byte, owner string, opts ...Option) (*Client, error) {
	key, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("github app key: %w", err)
	}
	c := &Client{base: DefaultBaseURL, http: &http.Client{Timeout: 3 * time.Minute}}
	for _, o := range opts {
		o(c)
	}
	c.app = &appAuth{
		appID:   appID,
		key:     key,
		owner:   owner,
		base:    c.base,
		http:    c.http,
		now:     time.Now,
		refresh: time.Minute, // re-mint a minute early to never send an expired token
	}
	return c, nil
}

// bearer returns the token to authenticate a REST call with: the App's cached
// installation token (refreshed on expiry) when the Client is App-backed, or the
// static PAT otherwise. This is the single seam the App swap runs through.
func (c *Client) bearer(ctx context.Context) (string, error) {
	if c.app == nil {
		return c.token, nil
	}
	return c.app.serverToken(ctx)
}

// serverToken returns the App's broad installation token, minting a fresh one
// when the cache is empty or within the refresh window of expiry.
func (a *appAuth) serverToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Before(a.tokenExp.Add(-a.refresh)) {
		return a.token, nil
	}
	tok, exp, err := a.mintLocked(ctx, nil)
	if err != nil {
		return "", err
	}
	a.token, a.tokenExp = tok, exp
	return tok, nil
}

// mintLocked mints an installation access token. With scope nil the token
// carries the installation's full grant (the server token); with a scope it is
// narrowed to those repositories and permissions (the mount token). The caller
// holds a.mu when this touches shared state; the token minting itself is
// stateless beyond resolving the installation id.
func (a *appAuth) mintLocked(ctx context.Context, scope map[string]any) (string, time.Time, error) {
	instID, err := a.installationID(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	jwt, err := a.signJWT()
	if err != nil {
		return "", time.Time{}, err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	status, err := a.doJWT(ctx, jwt, http.MethodPost,
		"/app/installations/"+strconv.FormatInt(instID, 10)+"/access_tokens", scope, &out)
	if err != nil {
		return "", time.Time{}, err
	}
	if status != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("mint installation token: unexpected status %d", status)
	}
	return out.Token, out.ExpiresAt, nil
}

// installationID resolves — and caches — the id of the App's installation on the
// owner org, so token minting knows which installation to mint for.
func (a *appAuth) installationID(ctx context.Context) (int64, error) {
	if a.instID != 0 {
		return a.instID, nil
	}
	jwt, err := a.signJWT()
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	status, err := a.doJWT(ctx, jwt, http.MethodGet, "/orgs/"+a.owner+"/installation", nil, &out)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("resolve installation for %s: unexpected status %d", a.owner, status)
	}
	if out.ID == 0 {
		return 0, fmt.Errorf("resolve installation for %s: no installation id", a.owner)
	}
	a.instID = out.ID
	return out.ID, nil
}

// signJWT builds the short-lived RS256 JWT that authenticates as the App itself
// (as opposed to an installation). GitHub caps its life at ten minutes; the iat
// is backdated a minute to tolerate clock skew.
func (a *appAuth) signJWT() (string, error) {
	now := a.now()
	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.appID,
	}
	signingInput, err := joinB64(header, claims)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// doJWT performs one App-authenticated (JWT Bearer) request. It is separate from
// Client.do because App calls carry the JWT, never an installation token.
func (a *appAuth) doJWT(ctx context.Context, jwt, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding %s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// parseRSAPrivateKey reads GitHub's App key, which is PKCS#1 ("RSA PRIVATE KEY")
// as downloaded, but PKCS#8 ("PRIVATE KEY") is accepted too so a re-encoded key
// still loads.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("not a PKCS#1 or PKCS#8 RSA key: %w", err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("PKCS#8 key is not RSA")
	}
	return key, nil
}

// joinB64 encodes a JWT's header and claims as the base64url "header.claims"
// signing input.
func joinB64(header, claims map[string]any) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c), nil
}

// ErrMintingUnavailable reports that this client cannot mint a scoped token.
// A personal access token authenticates but cannot produce a sub-token of
// itself: GitHub offers no such thing, and only a GitHub App can mint one.
var ErrMintingUnavailable = errors.New("github: minting a scoped token needs a GitHub App")

// Token is a credential with an end.
type Token struct {
	Value   string
	Expires time.Time
}

// MintRepoToken mints a credential that can write to one repository and to
// nothing else, for at most an hour (045l). It is the scope 009 designed and
// never called: every other mint passes nil and gets the installation's whole
// grant.
//
// contents:write is the entire permission set. Not administration, not a second
// repository: a leaked minted token can push to the reproduction it was minted
// for, until it expires, and can do nothing else anywhere.
func (c *Client) MintRepoToken(ctx context.Context, name string) (Token, error) {
	if c.app == nil {
		return Token{}, ErrMintingUnavailable
	}
	c.app.mu.Lock()
	defer c.app.mu.Unlock()
	value, expires, err := c.app.mintLocked(ctx, map[string]any{
		"repositories": []string{name},
		"permissions":  map[string]string{"contents": "write"},
	})
	if err != nil {
		return Token{}, err
	}
	return Token{Value: value, Expires: expires}, nil
}

// RevokeToken ends an installation token early. The token being revoked is what
// authenticates the call, so revocation needs no further credential and cannot
// be turned against anybody else's. A token already expired or already revoked
// answers 401, which is the outcome asked for rather than a failure.
func (c *Client) RevokeToken(ctx context.Context, token string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/installation/token", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	switch response.StatusCode {
	case http.StatusNoContent, http.StatusUnauthorized, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("revoke token: unexpected status %d", response.StatusCode)
	}
}

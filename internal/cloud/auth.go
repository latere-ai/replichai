// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cloud is the CLI's client for the platform: it signs in against
// auth.latere.ai with the device-code flow, stores the credential, and
// presents it to replichai's own API (045i).
//
// Authorization is not decided here. The token says who the caller is. What
// they may do is decided by the deployment's own access table, against a
// stored record, and never read out of a token.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	authjwt "latere.ai/x/pkg/authkit/jwt"
	"latere.ai/x/pkg/authkit/oidc"
)

// DefaultAuthURL is the identity provider the CLI signs in against.
const DefaultAuthURL = "https://auth.latere.ai"

// ClientID is replichai's own OAuth client. It is deliberately distinct from
// the latere CLI's: two programs sharing one client would share one token
// file, and a refresh that narrowed scopes in either would leave the other
// without a usable credential.
const ClientID = "replichai-cli"

// Audience is the service an actor token is minted for, so a credential
// captured in transit or in a shell history is valid for one service only.
const Audience = "replichai.latere.ai"

// Scopes are identity only. Nothing product-specific is requested, because
// nothing product-specific is decided by the token.
var Scopes = []string{"openid", "email", "profile", "offline_access"}

// ErrSignedOut reports that there is no usable credential and a sign-in is
// needed. It is deliberately distinct from a transport failure, because an
// auth outage must not be treated as a sign-out.
var ErrSignedOut = errors.New("not signed in")

// Token is the stored credential.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	Subject      string    `json:"subject,omitempty"`
	Email        string    `json:"email,omitempty"`
	Org          string    `json:"org,omitempty"`
}

// Expired reports whether the access token is at or past its expiry, with a
// minute of margin so a call does not start with a credential that expires
// while the call is in flight.
func (t Token) Expired(now time.Time) bool {
	return !t.Expiry.IsZero() && !now.Add(time.Minute).Before(t.Expiry)
}

// Store holds the credential in one file, readable only by the operator, in
// replichai's own configuration directory rather than the latere CLI's.
type Store struct{ Dir string }

// DefaultStore is the store under the operator's configuration directory.
func DefaultStore(home string) Store {
	return Store{Dir: filepath.Join(home, ".config", "replichai")}
}

// Path returns the credential file's path.
func (s Store) Path() string { return filepath.Join(s.Dir, "auth.json") }

// Save writes the credential with mode 0600.
func (s Store) Save(token Token) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	//nolint:gosec // this file is the credential store; writing the credential
	// into it is the point, and it is created 0600 below.
	body, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path(), append(body, '\n'), 0o600)
}

// Load reads the credential. A missing file returns ErrSignedOut rather than
// an I/O error, because not being signed in is the condition the caller acts
// on.
func (s Store) Load() (Token, error) {
	body, err := os.ReadFile(s.Path())
	if os.IsNotExist(err) {
		return Token{}, ErrSignedOut
	}
	if err != nil {
		return Token{}, err
	}
	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return Token{}, fmt.Errorf("parse %s: %w", s.Path(), err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return Token{}, ErrSignedOut
	}
	return token, nil
}

// Clear removes the credential. A credential that is already absent is not an
// error, so signing out twice succeeds.
func (s Store) Clear() error {
	err := os.Remove(s.Path())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Session holds a signed-in identity and presents it to the API.
type Session struct {
	// AuthURL is the identity provider.
	AuthURL string
	// Store is where the credential lives.
	Store Store
	// HTTP is the client used for token and actor calls.
	HTTP *http.Client
	// Now is the clock.
	Now func() time.Time
}

// New builds a session over the operator's own configuration directory.
func New(home, authURL string) *Session {
	if authURL == "" {
		authURL = DefaultAuthURL
	}
	return &Session{AuthURL: authURL, Store: DefaultStore(home), HTTP: httpClient, Now: time.Now}
}

func (s *Session) client() *oidc.Client {
	return oidc.New(oidc.Config{AuthURL: s.AuthURL, ClientID: ClientID, Scopes: Scopes})
}

func (s *Session) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Prompt describes what the operator must do to finish signing in.
type Prompt struct {
	VerificationURI string
	UserCode        string
	Interval        time.Duration
}

// Login runs the device-code flow. show is called once with the prompt the
// operator must act on; the call then blocks until they have done so or the
// code expires.
func (s *Session) Login(ctx context.Context, show func(Prompt)) (Token, error) {
	client := s.client()
	if client == nil {
		return Token{}, errors.New("cloud: no auth URL or client id")
	}
	response, err := client.DeviceAuth(ctx, nil)
	if err != nil {
		return Token{}, fmt.Errorf("start device login: %w", err)
	}
	if show != nil {
		uri := response.VerificationURIComplete
		if uri == "" {
			uri = response.VerificationURI
		}
		show(Prompt{VerificationURI: uri, UserCode: response.UserCode, Interval: time.Duration(response.Interval) * time.Second})
	}
	granted, err := client.DeviceAccessToken(ctx, response)
	if err != nil {
		return Token{}, fmt.Errorf("device login: %w", err)
	}
	token := s.tokenFrom(granted, Token{})
	if err := s.Store.Save(token); err != nil {
		return Token{}, err
	}
	return token, nil
}

// tokenFrom converts an OAuth grant into the stored shape. Where the response
// omits a new refresh token it keeps the previous one. Omitting it is ordinary
// OAuth behavior, and dropping the old one is a common cause of a working
// client signing itself out after a day.
func (s *Session) tokenFrom(granted *oauth2.Token, previous Token) Token {
	token := Token{
		AccessToken:  granted.AccessToken,
		RefreshToken: granted.RefreshToken,
		Expiry:       granted.Expiry,
		Issuer:       s.AuthURL,
		ClientID:     ClientID,
		Org:          previous.Org,
	}
	if token.RefreshToken == "" {
		token.RefreshToken = previous.RefreshToken
	}
	subject, email := identityFrom(granted.AccessToken)
	if subject == "" {
		subject, email = previous.Subject, previous.Email
	}
	token.Subject, token.Email = subject, email
	return token
}

// Current returns a usable credential, refreshing it when it has expired.
//
// A refresh that fails because the network is down, or because auth answered
// with a server error, leaves the stored credential in place and reports the
// failure. Only an explicit invalid_grant, meaning the credential is revoked
// or expired, clears it. A CLI that signed itself out on every transient
// network failure could not be trusted with a long batch run.
func (s *Session) Current(ctx context.Context) (Token, error) {
	token, err := s.Store.Load()
	if err != nil {
		return Token{}, err
	}
	if !token.Expired(s.now()) {
		return token, nil
	}
	if token.RefreshToken == "" {
		return Token{}, ErrSignedOut
	}
	client := s.client()
	granted, err := client.RefreshTokenContext(ctx, token.RefreshToken)
	if err != nil {
		if isInvalidGrant(err) {
			_ = s.Store.Clear()
			return Token{}, fmt.Errorf("%w: the saved credential was rejected; run `replichai login`", ErrSignedOut)
		}
		return Token{}, fmt.Errorf("refresh credential: %w", err)
	}
	refreshed := s.tokenFrom(granted, token)
	if err := s.Store.Save(refreshed); err != nil {
		return Token{}, err
	}
	return refreshed, nil
}

// SwitchOrg moves the saved credential to another organization context through
// the refresh grant, so switching does not need a second device login.
func (s *Session) SwitchOrg(ctx context.Context, org string) (Token, error) {
	token, err := s.Store.Load()
	if err != nil {
		return Token{}, err
	}
	if token.RefreshToken == "" {
		return Token{}, ErrSignedOut
	}
	granted, err := s.refreshWithOrg(ctx, token.RefreshToken, org)
	if err != nil {
		return Token{}, err
	}
	switched := s.tokenFrom(granted, token)
	switched.Org = org
	if err := s.Store.Save(switched); err != nil {
		return Token{}, err
	}
	return switched, nil
}

func (s *Session) refreshWithOrg(ctx context.Context, refresh, org string) (*oauth2.Token, error) {
	// The body is built with url.Values so a refresh token or an
	// organization id containing a reserved character reaches the provider
	// as one value rather than as extra fields.
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {ClientID}}
	if org != "" {
		form.Set("organization_id", org)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.AuthURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("switch organization: %s", response.Status)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil, err
	}
	token := &oauth2.Token{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken}
	if body.ExpiresIn > 0 {
		token.Expiry = s.now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return token, nil
}

// Bearer returns the credential to present to replichai's API. It prefers a
// short-lived actor token minted for this service, so a credential captured
// in transit is valid for minutes and for one audience only. Where the
// provider does not mint one it falls back to the stored access token and
// reports the fallback in the second return value, so a caller can tell when
// the short-lived credential was not obtained.
func (s *Session) Bearer(ctx context.Context, ttl time.Duration) (string, bool, error) {
	token, err := s.Current(ctx)
	if err != nil {
		return "", false, err
	}
	actor, err := s.mintActor(ctx, token.AccessToken, ttl)
	if err != nil {
		// A provider that does not mint an audience-scoped token has not
		// failed the sign-in. The stored credential still authenticates, and
		// the caller is told the short-lived one was not obtained.
		//nolint:nilerr // the fallback is the documented behavior, and it is reported
		return token.AccessToken, false, nil
	}
	return actor, true, nil
}

func (s *Session) mintActor(ctx context.Context, bearer string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	payload, err := json.Marshal(map[string]any{"audience": Audience, "ttl_seconds": int(ttl.Seconds())})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.AuthURL+"/actor-tokens", strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := s.HTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		return "", fmt.Errorf("actor-tokens: %s", response.Status)
	}
	var body struct {
		ActorToken string `json:"actor_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.ActorToken == "" {
		return "", errors.New("actor-tokens: empty response")
	}
	return body.ActorToken, nil
}

// Logout removes the stored credential.
func (s *Session) Logout() error { return s.Store.Clear() }

// isInvalidGrant reports whether the provider rejected the credential itself,
// as opposed to failing to answer.
func isInvalidGrant(err error) bool {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		if retrieve.ErrorCode == "invalid_grant" {
			return true
		}
		return retrieve.Response != nil && retrieve.Response.StatusCode == http.StatusBadRequest &&
			strings.Contains(string(retrieve.Body), "invalid_grant")
	}
	return strings.Contains(err.Error(), "invalid_grant")
}

// identityFrom reads the subject and email from a JWT's payload without
// verifying the signature. The CLI is not a resource server. It reads its
// own credential to print who is signed in, and the server verifies the
// token.
func identityFrom(raw string) (subject, email string) {
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if authjwt.DecodePayload(raw, &claims) != nil {
		return "", ""
	}
	return claims.Sub, claims.Email
}

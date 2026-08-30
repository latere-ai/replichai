// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cloud is the terminal's side of the platform: signing in against
// auth.latere.ai with the device-code flow, keeping the credential, and
// presenting it to replichai's own API (045i).
//
// Authorisation is not here. The token says who the caller is; what they may
// do is decided by the deployment's own table, against a record and never read
// out of a token.
package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"latere.ai/x/pkg/oidc"
)

// DefaultAuthURL is the identity provider the CLI signs in against.
const DefaultAuthURL = "https://auth.latere.ai"

// ClientID is replichai's own OAuth client. It is deliberately not the
// latere CLI's: two programs sharing one client would share one token file,
// and a refresh that narrowed scopes in either would strand the other.
const ClientID = "replichai-cli"

// Audience is what an actor token is minted for, so a credential captured in
// transit or in a shell history is bounded to one service.
const Audience = "replichai.latere.ai"

// Scopes are identity only. Nothing product-specific is requested, because
// nothing product-specific is decided by the token.
var Scopes = []string{"openid", "email", "profile", "offline_access"}

// ErrSignedOut reports that there is no usable credential and a sign-in is
// needed. It is deliberately distinct from a transport failure: an auth outage
// is not a sign-out.
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

// Expired reports whether the access token is at or past its life, with a
// minute of margin so a call does not start with a credential that dies in
// flight.
func (t Token) Expired(now time.Time) bool {
	return !t.Expiry.IsZero() && !now.Add(time.Minute).Before(t.Expiry)
}

// Store is where the credential is kept: one file, private to the operator,
// in replichai's own configuration directory and not the latere CLI's.
type Store struct{ Dir string }

// DefaultStore is the store under the operator's configuration directory.
func DefaultStore(home string) Store {
	return Store{Dir: filepath.Join(home, ".config", "replichai")}
}

// Path is the credential file.
func (s Store) Path() string { return filepath.Join(s.Dir, "auth.json") }

// Save writes the credential, private to the operator.
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

// Load reads the credential. A missing file is ErrSignedOut rather than an
// I/O error, because "you are not signed in" is what the caller acts on.
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

// Clear removes the credential. Removing one that is not there is not an
// error: signing out twice is not a failure.
func (s Store) Clear() error {
	err := os.Remove(s.Path())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Session is a signed-in identity, and the thing that presents it.
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
	return &Session{AuthURL: authURL, Store: DefaultStore(home), HTTP: http.DefaultClient, Now: time.Now}
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

// Prompt is what the operator has to do to finish signing in.
type Prompt struct {
	VerificationURI string
	UserCode        string
	Interval        time.Duration
}

// Login runs the device-code flow. show is called once with what the operator
// must do; the call then blocks until they have done it or the code expires.
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

// tokenFrom folds an OAuth grant into the stored shape, keeping the previous
// refresh token where the response omits a new one — ordinary OAuth behaviour,
// and a common way a working client signs itself out after a day.
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

// Current returns a usable credential, refreshing it where it is due.
//
// A refresh that fails because the network is down, or because auth answered
// with a server error, leaves the stored credential in place and reports the
// failure. Only an explicit invalid_grant — the credential is genuinely
// revoked or expired — clears it. A CLI that signs itself out whenever a
// coffee-shop network hiccups is one people stop trusting with an hour-long
// batch.
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

// SwitchOrg moves the saved credential to another organisation context through
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
	form := "grant_type=refresh_token&refresh_token=" + refresh + "&client_id=" + ClientID
	if org != "" {
		form += "&organization_id=" + org
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.AuthURL+"/token", strings.NewReader(form))
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
		return nil, fmt.Errorf("switch organisation: %s", response.Status)
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

// Bearer is the credential to present to replichai's API. It prefers a
// short-lived actor token minted for this service, so a credential captured in
// transit is bounded to minutes and to one audience, and falls back to the
// stored access token where the provider does not mint one — the fallback is
// reported rather than silent, because a security property that quietly stops
// holding is worse than one never claimed.
func (s *Session) Bearer(ctx context.Context, ttl time.Duration) (string, bool, error) {
	token, err := s.Current(ctx)
	if err != nil {
		return "", false, err
	}
	actor, err := s.mintActor(ctx, token.AccessToken, ttl)
	if err != nil {
		// A provider that will not mint an audience-scoped token is not a
		// failure to sign in: the stored credential still authenticates, and
		// the caller is told the short-lived one was not obtained.
		//nolint:nilerr // the fallback is the documented behaviour, and it is reported
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

// identityFrom reads the subject and email out of a JWT's payload without
// verifying it. The CLI is not a resource server: it is reading its own
// credential to print who is signed in, and the server verifies for real.
func identityFrom(raw string) (subject, email string) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", ""
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &claims); err != nil {
		return "", ""
	}
	return claims.Sub, claims.Email
}

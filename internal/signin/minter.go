// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// PlatformAudience is the audience the Latere platform's API verifies.
const PlatformAudience = "api.latere.ai"

// actorTTL is how long an actor token lives, the most Auth mints, and
// actorKeep how long one is reused: short of its expiry, so a call handed it
// does not carry an expired token.
const (
	actorTTL  = 5 * time.Minute
	actorKeep = 4 * time.Minute
)

// renewBefore is how long before its expiry an access token is replaced.
const renewBefore = time.Minute

// Config is Auth as the server signs people in to it.
type Config struct {
	// Issuer is Latere Auth's origin, whose /token and /actor-tokens the
	// minter calls.
	Issuer string
	// ClientID and ClientSecret are the server's own sign-in client.
	ClientID     string
	ClientSecret string
	// HTTP is the client every call goes through.
	HTTP *http.Client
}

// Minter gives a bearer for the Latere platform's API that acts as one
// person, from the sign-in the Store keeps for them.
type Minter struct {
	store  Store
	config Config
	now    func() time.Time

	mu     sync.Mutex
	people map[string]*person
}

// person is one subject's cached tokens and the lock that makes one refresh
// at a time per person in this process. Across processes the Store's lock
// does the same.
type person struct {
	mu          sync.Mutex
	access      string
	accessUntil time.Time
	actor       string
	actorUntil  time.Time
}

// NewMinter builds a minter over a store.
func NewMinter(store Store, config Config) *Minter {
	config.Issuer = strings.TrimRight(config.Issuer, "/")
	return &Minter{store: store, config: config, now: time.Now, people: map[string]*person{}}
}

func (m *Minter) person(subject string) *person {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.people[subject]
	if !ok {
		p = &person{}
		m.people[subject] = p
	}
	return p
}

// Token gives a bearer for the platform's API acting as subject.
func (m *Minter) Token(ctx context.Context, subject string) (string, error) {
	p := m.person(subject)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := m.now()
	if p.actor != "" && now.Before(p.actorUntil) {
		return p.actor, nil
	}
	if p.access == "" || !now.Before(p.accessUntil.Add(-renewBefore)) {
		if err := m.refresh(ctx, subject, p); err != nil {
			return "", err
		}
	}
	actor, err := m.mintActor(ctx, p.access)
	if err != nil {
		return "", err
	}
	p.actor, p.actorUntil = actor, m.now().Add(actorKeep)
	return actor, nil
}

// TokenFunc is Token for one person, in the shape the runtime's composition
// root takes.
func (m *Minter) TokenFunc(subject string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) { return m.Token(ctx, subject) }
}

// refresh trades the stored refresh token for an access token and stores the
// refresh token Auth rotated it to. A refresh token Auth refuses is
// forgotten, so the person is asked to sign in again rather than refused on
// every later call.
func (m *Minter) refresh(ctx context.Context, subject string, p *person) error {
	var refused bool
	err := m.store.Rotate(ctx, subject, func(current string) (string, error) {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current}}
		var body struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
		}
		status, err := m.post(ctx, "/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "", true, &body)
		if status == http.StatusBadRequest || status == http.StatusUnauthorized {
			refused = true
			return "", fmt.Errorf("%w: Auth refused the stored sign-in", ErrNoSignIn)
		}
		if err != nil {
			return "", fmt.Errorf("refresh the Latere sign-in: %w", err)
		}
		if body.AccessToken == "" {
			return "", errors.New("refresh the Latere sign-in: Auth gave no access token")
		}
		lifetime := time.Duration(body.ExpiresIn) * time.Second
		if lifetime <= 0 {
			lifetime = 15 * time.Minute
		}
		p.access, p.accessUntil = body.AccessToken, m.now().Add(lifetime)
		if body.RefreshToken == "" {
			return current, nil
		}
		return body.RefreshToken, nil
	})
	if refused {
		if forgetErr := m.store.Forget(context.WithoutCancel(ctx), subject); forgetErr != nil {
			return errors.Join(err, forgetErr)
		}
	}
	return err
}

// mintActor asks Auth for a token addressed to the platform's API, from the
// person's access token.
func (m *Minter) mintActor(ctx context.Context, access string) (string, error) {
	payload, err := json.Marshal(map[string]any{"audience": PlatformAudience, "ttl_seconds": int(actorTTL.Seconds())})
	if err != nil {
		return "", err
	}
	var body struct {
		ActorToken string `json:"actor_token"`
	}
	if _, err := m.post(ctx, "/actor-tokens", strings.NewReader(string(payload)), "application/json", access, false, &body); err != nil {
		return "", fmt.Errorf("mint a Latere platform token: %w", err)
	}
	if body.ActorToken == "" {
		return "", errors.New("mint a Latere platform token: Auth gave none")
	}
	return body.ActorToken, nil
}

// post sends one request to Auth and decodes its answer. bearer is the
// person's access token for a mint; asClient authenticates as the server's
// sign-in client instead.
func (m *Minter) post(ctx context.Context, path string, body io.Reader, contentType, bearer string, asClient bool, out any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.Issuer+path, body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", contentType)
	if asClient {
		request.SetBasicAuth(url.QueryEscape(m.config.ClientID), url.QueryEscape(m.config.ClientSecret))
	} else {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := m.config.HTTP.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode/100 != 2 {
		return response.StatusCode, fmt.Errorf("Auth answered %d", response.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return response.StatusCode, fmt.Errorf("decode Auth's answer: %w", err)
	}
	return response.StatusCode, nil
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	xotel "latere.ai/x/pkg/otel"
)

// httpClient is this package's outbound client. Its transport is instrumented,
// so every request starts a client span and carries the W3C trace context to
// the other side. http.DefaultClient is deliberately left alone: replacing its
// transport would reach every package in the process, including ones that have
// made no such decision.
var httpClient = xotel.HTTPClient()

// Endpoints are the issuer-advertised URLs this service uses.
type Endpoints struct {
	// UserInfo is where the email lives when the ID token does not carry it
	// (latere auth keeps PII out of the token).
	UserInfo string
	// EndSession is the issuer's RP-initiated logout. Without it, signing out
	// clears this app's cookie and leaves the identity provider's session
	// standing — so the next sign-in silently re-issues a code for the same
	// account, and nobody can switch profiles or actually leave a shared
	// machine. Empty when the issuer advertises none.
	EndSession string
}

// Discover reads the endpoints this service needs from an issuer's OIDC
// discovery document. One request for both: they are two fields of one
// document, and two round trips to fetch them would be two ways to fail.
func Discover(ctx context.Context, issuer string) (Endpoints, error) {
	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Endpoints{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Endpoints{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Endpoints{}, fmt.Errorf("discovery %s: status %d", url, resp.StatusCode)
	}
	var doc struct {
		UserInfoEndpoint   string `json:"userinfo_endpoint"`
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return Endpoints{}, err
	}
	if doc.UserInfoEndpoint == "" {
		return Endpoints{}, fmt.Errorf("discovery %s: no userinfo_endpoint", url)
	}
	return Endpoints{UserInfo: doc.UserInfoEndpoint, EndSession: doc.EndSessionEndpoint}, nil
}

// HTTPUserInfo builds a UserInfoFunc that GETs the OIDC userinfo endpoint with
// the access token and returns the profile: the email, and the two display
// claims (006m). Roles, scopes, and org claims are not decoded at all — the
// struct is the filter.
func HTTPUserInfo(endpoint string) UserInfoFunc {
	return func(ctx context.Context, accessToken string) (Profile, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return Profile{}, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := httpClient.Do(req)
		if err != nil {
			return Profile{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return Profile{}, fmt.Errorf("userinfo: status %d", resp.StatusCode)
		}
		var claims struct {
			Email             string `json:"email"`
			Name              string `json:"name"`
			PreferredUsername string `json:"preferred_username"`
			Picture           string `json:"picture"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
			return Profile{}, err
		}
		name := strings.TrimSpace(claims.Name)
		if name == "" {
			name = strings.TrimSpace(claims.PreferredUsername)
		}
		return Profile{Email: claims.Email, Name: name, Picture: claims.Picture}, nil
	}
}

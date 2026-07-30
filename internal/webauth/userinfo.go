package webauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DiscoverUserInfo reads the userinfo_endpoint from an issuer's OIDC discovery
// document, so the login knows where to fetch the email that the ID token may
// not carry.
func DiscoverUserInfo(ctx context.Context, issuer string) (string, error) {
	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery %s: status %d", url, resp.StatusCode)
	}
	var doc struct {
		UserInfoEndpoint string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", err
	}
	if doc.UserInfoEndpoint == "" {
		return "", fmt.Errorf("discovery %s: no userinfo_endpoint", url)
	}
	return doc.UserInfoEndpoint, nil
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
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return Profile{}, err
		}
		defer resp.Body.Close()
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

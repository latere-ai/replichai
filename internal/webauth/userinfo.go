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
// the access token and returns the email and its verification.
func HTTPUserInfo(endpoint string) UserInfoFunc {
	return func(ctx context.Context, accessToken string) (string, bool, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", false, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", false, fmt.Errorf("userinfo: status %d", resp.StatusCode)
		}
		var claims struct {
			Email         string `json:"email"`
			EmailVerified any    `json:"email_verified"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
			return "", false, err
		}
		return claims.Email, verifiedFromAny(claims.EmailVerified), nil
	}
}

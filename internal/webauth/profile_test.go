// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"latere.ai/x/pkg/authkit/oidc"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// callbackSession drives one callback with the given userinfo and returns the
// session it sealed.
func callbackSession(t *testing.T, id oidc.User, ui UserInfoFunc) Session {
	t.Helper()
	codec, _ := NewCodec(testKey)
	l := NewLogin(fakeOIDC{identity: id}, codec, access.NewMemStore(), "https://replichai.latere.ai", false, ui)
	flow, _ := l.codec.Seal(flowState{State: "s", Verifier: "v", Next: "/"}, time.Hour)
	r := httptest.NewRequest("GET", "/callback?state=s&code=c", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
	w := httptest.NewRecorder()
	l.HandleCallback(w, r)

	sealed := findCookie(w, cookieName(sessionCookie, false))
	if sealed == "" {
		t.Fatal("session cookie was not set")
	}
	var s Session
	if err := l.codec.Open(sealed, &s); err != nil {
		t.Fatalf("open session: %v", err)
	}
	return s
}

// AC1: the callback keeps name and picture, preferring name over
// preferred_username, and still signs in when /userinfo carries neither.
func TestCallbackKeepsDisplayClaims(t *testing.T) {
	id := oidc.User{Sub: "uuid-1", Raw: map[string]any{}}

	t.Run("name and picture", func(t *testing.T) {
		s := callbackSession(t, id, func(context.Context, string) (Profile, error) {
			return Profile{Email: "hi@changkun.de", Name: "Changkun Ou", Picture: "https://auth.latere.ai/avatar/1.png"}, nil
		})
		if s.Name != "Changkun Ou" || s.Picture != "https://auth.latere.ai/avatar/1.png" {
			t.Errorf("session = %+v, want the display claims carried", s)
		}
	})

	t.Run("no display claims still signs in", func(t *testing.T) {
		s := callbackSession(t, id, func(context.Context, string) (Profile, error) {
			return Profile{Email: "hi@changkun.de"}, nil
		})
		if s.Sub != "uuid-1" || s.Email != "hi@changkun.de" {
			t.Errorf("session = %+v, want a sign-in on email alone", s)
		}
		if s.Name != "" || s.Picture != "" {
			t.Errorf("session = %+v, want no invented display data", s)
		}
	})

	t.Run("userinfo failure costs the name, never the sign-in", func(t *testing.T) {
		withEmail := oidc.User{Sub: "uuid-1", Email: "hi@changkun.de", Raw: map[string]any{}}
		s := callbackSession(t, withEmail, func(context.Context, string) (Profile, error) {
			return Profile{}, context.DeadlineExceeded
		})
		if s.Sub != "uuid-1" || s.Email != "hi@changkun.de" {
			t.Errorf("session = %+v, want the sign-in to survive a userinfo failure", s)
		}
	})
}

// AC1: name falls back to preferred_username, and both are decoded from the
// wire. The target struct has no field for a role claim, so one is never
// parsed.
func TestHTTPUserInfoDecodesDisplayClaims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"email":              "hi@changkun.de",
			"preferred_username": "changkun",
			"picture":            "https://auth.latere.ai/a.png",
			"roles":              []string{"platform-admin"},
		})
	}))
	defer srv.Close()

	p, err := HTTPUserInfo(srv.URL)(context.Background(), "tok")
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	want := Profile{Email: "hi@changkun.de", Name: "changkun", Picture: "https://auth.latere.ai/a.png"}
	if p != want {
		t.Errorf("profile = %+v, want %+v", p, want)
	}
}

// AC4: an avatar URL is rendered only when it is absolute https. A javascript:
// or data: value from the issuer must never reach an <img src>.
func TestSafePicture(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://auth.latere.ai/a.png", "https://auth.latere.ai/a.png"},
		{" https://auth.latere.ai/a.png ", "https://auth.latere.ai/a.png"},
		{"http://auth.latere.ai/a.png", ""},
		{"javascript:alert(1)", ""},
		{"data:image/png;base64,AAAA", ""},
		{"/relative.png", ""},
		{"", ""},
	} {
		if got := safePicture(tc.in); got != tc.want {
			t.Errorf("safePicture(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// AC2: /me reports the display claims when present, omits them when absent, and
// reports them for an ungranted principal too, so the not-invited screen can
// address the person by name.
func TestMeReportsDisplayClaims(t *testing.T) {
	me := Me(access.NewMemStore(), nil, nil)

	t.Run("ungranted principal still gets a name", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/me", nil)
		r = r.WithContext(httpapi.WithIdentity(r.Context(), httpapi.Identity{
			Sub: "uuid-1", Email: "hi@changkun.de", Name: "Changkun Ou", Picture: "https://auth.latere.ai/a.png",
		}))
		w := httptest.NewRecorder()
		me(w, r)

		var body map[string]any
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["tier"] != "none" {
			t.Errorf("tier = %v, want none", body["tier"])
		}
		if body["name"] != "Changkun Ou" || body["picture"] != "https://auth.latere.ai/a.png" {
			t.Errorf("me = %v, want the display claims reported", body)
		}
	})

	t.Run("absent claims are omitted, not empty strings", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/me", nil)
		r = r.WithContext(httpapi.WithIdentity(r.Context(), httpapi.Identity{Sub: "uuid-2", Email: "other@example.com"}))
		w := httptest.NewRecorder()
		me(w, r)

		var body map[string]any
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := body["name"]; ok {
			t.Errorf("me = %v, want no name key", body)
		}
		if _, ok := body["picture"]; ok {
			t.Errorf("me = %v, want no picture key", body)
		}
	})
}

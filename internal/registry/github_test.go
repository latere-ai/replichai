// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package registry

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/publish"
)

// TestGitHubSourceThroughRegistry drives the registry end to end over a fake
// GitHub API: list the org, read a manifest, relate by identity.
func TestGitHubSourceThroughRegistry(t *testing.T) {
	manifestJSON, _ := json.Marshal(manifestOf("repo-a", "10.1145/x", "a-paper", t1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/orgs/open-hai/repos"):
			_ = json.MarshalWrite(w, []map[string]string{{"name": "repo-a"}})
		case r.URL.Path == "/repos/open-hai/repo-a/contents/"+publish.ManifestFile:
			_ = json.MarshalWrite(w, map[string]any{
				"content":  base64.StdEncoding.EncodeToString(manifestJSON),
				"encoding": "base64",
			})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	src := NewGitHubSource(github.New("token", github.WithBaseURL(server.URL)), "open-hai")
	r := New(src)
	priors, err := r.Lookup(context.Background(), doiID("10.1145/x"))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(priors) != 1 || priors[0].Repo != "open-hai/repo-a" {
		t.Errorf("priors = %+v", priors)
	}
}

// TestGitHubSourceAbsentManifestIsNoManifest checks that a forge answering not
// found yields ErrNoManifest, since the repository's run has not finalized,
// and that a forge that fails to answer yields a read failure. The publish
// gate tells an owner two different things depending on which it was.
func TestGitHubSourceAbsentManifestIsNoManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/open-hai/unreachable/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	src := NewGitHubSource(github.New("token", github.WithBaseURL(server.URL)), "open-hai")

	if _, err := src.Manifest(context.Background(), "unfinished"); !errors.Is(err, ErrNoManifest) {
		t.Errorf("absent manifest: err = %v, want ErrNoManifest", err)
	}
	if _, err := src.Manifest(context.Background(), "unreachable"); err == nil || errors.Is(err, ErrNoManifest) {
		t.Errorf("forge failure: err = %v, want a read failure", err)
	}
}

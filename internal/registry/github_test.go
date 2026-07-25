package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "repo-a"}})
		case r.URL.Path == "/repos/open-hai/repo-a/contents/"+publish.ManifestFile:
			_ = json.NewEncoder(w).Encode(map[string]any{
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

package registry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/publish"
)

// GitHubSource reads reproductions from a GitHub organisation through
// internal/github — the production source behind the registry.
type GitHubSource struct {
	client *github.Client
	owner  string
}

// NewGitHubSource returns a source over an organisation.
func NewGitHubSource(client *github.Client, owner string) *GitHubSource {
	return &GitHubSource{client: client, owner: owner}
}

// List enumerates the organisation's repositories.
func (s *GitHubSource) List(ctx context.Context) ([]string, error) {
	return s.client.ListRepos(ctx, s.owner)
}

// Manifest reads and parses one repository's replication.json. A repository
// without one is not a reproduction and is reported as an error, which the
// registry skips.
func (s *GitHubSource) Manifest(ctx context.Context, repo string) (publish.Manifest, error) {
	raw, ok, err := s.client.GetFileContent(ctx, s.owner, repo, publish.ManifestFile)
	if err != nil {
		return publish.Manifest{}, err
	}
	if !ok {
		return publish.Manifest{}, fmt.Errorf("%s has no %s", repo, publish.ManifestFile)
	}
	var m publish.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return publish.Manifest{}, fmt.Errorf("malformed manifest in %s: %w", repo, err)
	}
	return m, nil
}

// File reads one file from a repository through the forge, via the raw media
// type so a large artifact (a full transcript.jsonl, over the 1MB inline limit)
// still comes back.
func (s *GitHubSource) File(ctx context.Context, repo, path string) ([]byte, bool, error) {
	return s.client.GetFileRaw(ctx, s.owner, repo, path)
}

// Tree lists a repository's file paths at its head commit.
func (s *GitHubSource) Tree(ctx context.Context, repo string) ([]string, error) {
	head, err := s.client.HeadCommit(ctx, s.owner, repo)
	if err != nil {
		return nil, err
	}
	return s.client.TreePaths(ctx, s.owner, repo, head)
}

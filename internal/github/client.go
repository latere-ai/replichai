// Package github is a small client for the handful of operations publishing a
// reproduction needs: create a private repository, invite a collaborator, read
// the head commit, and make a repository public. It is hand-rolled over
// net/http rather than a full SDK to keep the dependency list lean — it covers
// only these endpoints, and none of them is on a hot path.
//
// All of these run server-side with the organisation's token. None of them
// belongs inside the sandbox; the sandbox gets only a scoped mount token (006f).
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is GitHub's REST API root.
const DefaultBaseURL = "https://api.github.com"

// ErrRepoExists reports that a repository of that name already exists — a
// republish, not a failure. The caller fetches it with GetRepo.
var ErrRepoExists = errors.New("repository already exists")

// Repo is the subset of a repository this package reads.
type Repo struct {
	Owner   string `json:"-"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Private bool   `json:"private"`
}

// Client talks to the GitHub REST API with one token.
type Client struct {
	token string
	base  string
	http  *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API root. Tests point it at a local server.
func WithBaseURL(base string) Option { return func(c *Client) { c.base = base } }

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a client authenticating with token.
func New(token string, opts ...Option) *Client {
	c := &Client{token: token, base: DefaultBaseURL, http: &http.Client{Timeout: 30 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// CreateRepo creates a private (or public) repository in an organisation. A
// name that already exists returns ErrRepoExists rather than failing, so a
// republish is the caller's decision.
func (c *Client) CreateRepo(ctx context.Context, owner, name string, private bool) (Repo, error) {
	body := map[string]any{
		"name":      name,
		"private":   private,
		"auto_init": true,
	}
	var repo Repo
	status, err := c.do(ctx, http.MethodPost, "/orgs/"+owner+"/repos", body, &repo)
	if err != nil {
		return Repo{}, err
	}
	switch status {
	case http.StatusCreated:
		repo.Owner = owner
		return repo, nil
	case http.StatusUnprocessableEntity:
		return Repo{}, ErrRepoExists
	default:
		return Repo{}, fmt.Errorf("create %s/%s: unexpected status %d", owner, name, status)
	}
}

// ListRepos returns the names of every repository in an organisation, following
// pagination. The registry (003) enumerates the organisation this way.
func (c *Client) ListRepos(ctx context.Context, owner string) ([]string, error) {
	var names []string
	for page := 1; ; page++ {
		var repos []struct {
			Name string `json:"name"`
		}
		status, err := c.do(ctx, http.MethodGet,
			fmt.Sprintf("/orgs/%s/repos?per_page=100&page=%d", owner, page), nil, &repos)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("list repos of %s: unexpected status %d", owner, status)
		}
		for _, r := range repos {
			names = append(names, r.Name)
		}
		if len(repos) < 100 {
			return names, nil
		}
	}
}

// GetRepo fetches a repository, so a caller can recover after ErrRepoExists.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (Repo, error) {
	var repo Repo
	status, err := c.do(ctx, http.MethodGet, "/repos/"+owner+"/"+name, nil, &repo)
	if err != nil {
		return Repo{}, err
	}
	if status != http.StatusOK {
		return Repo{}, fmt.Errorf("get %s/%s: unexpected status %d", owner, name, status)
	}
	repo.Owner = owner
	return repo, nil
}

// Invite adds login as a collaborator. For a user outside the organisation this
// creates an invitation they must accept; for an existing collaborator it is a
// no-op. It never requests any scope from the invited user.
func (c *Client) Invite(ctx context.Context, owner, name, login string) error {
	status, err := c.do(ctx, http.MethodPut,
		"/repos/"+owner+"/"+name+"/collaborators/"+url.PathEscape(login), map[string]any{}, nil)
	if err != nil {
		return err
	}
	// 201 = invitation created, 204 = already a collaborator.
	if status != http.StatusCreated && status != http.StatusNoContent {
		return fmt.Errorf("invite %s to %s/%s: unexpected status %d", login, owner, name, status)
	}
	return nil
}

// HeadCommit returns the sha of the latest commit on the repository's default
// branch — the commit a reproduction's history ends at.
func (c *Client) HeadCommit(ctx context.Context, owner, name string) (string, error) {
	var commits []struct {
		SHA string `json:"sha"`
	}
	status, err := c.do(ctx, http.MethodGet, "/repos/"+owner+"/"+name+"/commits?per_page=1", nil, &commits)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("head of %s/%s: unexpected status %d", owner, name, status)
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("%s/%s has no commits", owner, name)
	}
	return commits[0].SHA, nil
}

// TreePaths lists the blob paths committed on a ref (a branch or a commit sha),
// recursively — the committed tree the post-run verifier checks against the
// bounded-workspace rule.
func (c *Client) TreePaths(ctx context.Context, owner, name, ref string) ([]string, error) {
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	status, err := c.do(ctx, http.MethodGet,
		"/repos/"+owner+"/"+name+"/git/trees/"+ref+"?recursive=1", nil, &tree)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("tree of %s/%s@%s: unexpected status %d", owner, name, ref, status)
	}
	if tree.Truncated {
		return nil, fmt.Errorf("tree of %s/%s is too large to list in one page", owner, name)
	}
	paths := make([]string, 0, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type == "blob" {
			paths = append(paths, e.Path)
		}
	}
	return paths, nil
}

// PutFile commits a single file, creating or updating it. The server uses it to
// add replication.json after the agent's history — the manifest records facts
// the agent never had (the method digest, the head commit, the repository name).
func (c *Client) PutFile(ctx context.Context, owner, name, path, message string, content []byte) error {
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	// If the file already exists (a republish), its blob sha is required; fetch
	// it and include it so the update is accepted rather than rejected.
	if sha, ok, err := c.fileSHA(ctx, owner, name, path); err != nil {
		return err
	} else if ok {
		body["sha"] = sha
	}
	status, err := c.do(ctx, http.MethodPut,
		"/repos/"+owner+"/"+name+"/contents/"+path, body, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("put %s in %s/%s: unexpected status %d", path, owner, name, status)
	}
	return nil
}

// GetFileContent returns the decoded bytes of a file, or ok=false if it is
// absent — so a caller can compare against what it is about to write and skip a
// no-op commit.
func (c *Client) GetFileContent(ctx context.Context, owner, name, path string) ([]byte, bool, error) {
	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	status, err := c.do(ctx, http.MethodGet, "/repos/"+owner+"/"+name+"/contents/"+path, nil, &file)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusNotFound {
		return nil, false, nil
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("read %s in %s/%s: unexpected status %d", path, owner, name, status)
	}
	if file.Encoding != "base64" {
		return nil, false, fmt.Errorf("%s in %s/%s has unexpected encoding %q", path, owner, name, file.Encoding)
	}
	// GitHub wraps base64 content at 60 columns; strip the newlines to decode.
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return nil, false, fmt.Errorf("decoding %s: %w", path, err)
	}
	return decoded, true, nil
}

// fileSHA returns the blob sha of an existing file, or ok=false if it is absent.
func (c *Client) fileSHA(ctx context.Context, owner, name, path string) (string, bool, error) {
	var file struct {
		SHA string `json:"sha"`
	}
	status, err := c.do(ctx, http.MethodGet, "/repos/"+owner+"/"+name+"/contents/"+path, nil, &file)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	if status != http.StatusOK {
		return "", false, fmt.Errorf("stat %s in %s/%s: unexpected status %d", path, owner, name, status)
	}
	return file.SHA, true, nil
}

// SetPublic makes a repository public. It is deliberately one-way: this package
// offers no SetPrivate.
func (c *Client) SetPublic(ctx context.Context, owner, name string) error {
	status, err := c.do(ctx, http.MethodPatch, "/repos/"+owner+"/"+name,
		map[string]any{"private": false, "visibility": "public"}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("make %s/%s public: unexpected status %d", owner, name, status)
	}
	return nil
}

// do performs one request, decoding a JSON response into out when non-nil, and
// returns the status code.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if out != nil && len(payload) > 0 && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(payload, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding %s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

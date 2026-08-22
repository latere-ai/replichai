package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
)

// CommitFiles writes a set of files in a single commit on the default branch,
// using the Git Data API (blob → tree → commit → ref). Unlike the Contents API
// it is atomic across many files and has no per-file inline limit, so a chunked,
// multi-megabyte transcript lands as one commit rather than a dozen.
//
// It is idempotent: if every file already matches (by blob sha), it commits
// nothing and returns false.
func (c *Client) CommitFiles(ctx context.Context, owner, name, message string, files map[string][]byte) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}

	// Nothing to do if every file already holds exactly this content.
	allSame := true
	for path, content := range files {
		unchanged, err := c.FileUnchanged(ctx, owner, name, path, content)
		if err != nil {
			return false, err
		}
		if !unchanged {
			allSame = false
			break
		}
	}
	if allSame {
		return false, nil
	}

	branch, err := c.defaultBranch(ctx, owner, name)
	if err != nil {
		return false, err
	}
	base := "/repos/" + owner + "/" + name

	// A blob per file. Blobs are content-addressed, so they are made once and
	// survive a retry unchanged.
	type treeEntry struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	entries := make([]treeEntry, 0, len(files))
	for path, content := range files {
		var blob struct {
			SHA string `json:"sha"`
		}
		body := map[string]any{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64"}
		if _, err := c.expect(ctx, http.MethodPost, base+"/git/blobs", body, &blob, http.StatusCreated); err != nil {
			return false, err
		}
		entries = append(entries, treeEntry{Path: path, Mode: "100644", Type: "blob", SHA: blob.SHA})
	}

	// Read the head, build a commit on it, move the branch — and if the branch
	// moved underneath us, do it again on the new head. GitHub answers a ref
	// update that is not a fast-forward with 422, which happens for two ordinary
	// reasons: its ref reads can lag a Contents API write by a moment, and this
	// repository writes the manifest immediately before the transcript. One
	// retry covers both. It does not make concurrent drivers safe — two of them
	// would simply both commit — and the job lease is what prevents that (044c).
	for attempt := 0; ; attempt++ {
		var ref struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if _, err := c.expect(ctx, http.MethodGet, base+"/git/ref/heads/"+branch, nil, &ref, http.StatusOK); err != nil {
			return false, err
		}
		var commit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		}
		if _, err := c.expect(ctx, http.MethodGet, base+"/git/commits/"+ref.Object.SHA, nil, &commit, http.StatusOK); err != nil {
			return false, err
		}
		var tree struct {
			SHA string `json:"sha"`
		}
		if _, err := c.expect(ctx, http.MethodPost, base+"/git/trees",
			map[string]any{"base_tree": commit.Tree.SHA, "tree": entries}, &tree, http.StatusCreated); err != nil {
			return false, err
		}
		var newCommit struct {
			SHA string `json:"sha"`
		}
		if _, err := c.expect(ctx, http.MethodPost, base+"/git/commits",
			map[string]any{"message": message, "tree": tree.SHA, "parents": []string{ref.Object.SHA}}, &newCommit, http.StatusCreated); err != nil {
			return false, err
		}
		status, err := c.expect(ctx, http.MethodPatch, base+"/git/refs/heads/"+branch,
			map[string]any{"sha": newCommit.SHA}, nil, http.StatusOK)
		if err == nil {
			return true, nil
		}
		if attempt > 0 || status != http.StatusUnprocessableEntity {
			return false, err
		}
	}
}

// defaultBranch returns the repository's default branch name.
func (c *Client) defaultBranch(ctx context.Context, owner, name string) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := c.expect(ctx, http.MethodGet, "/repos/"+owner+"/"+name, nil, &repo, http.StatusOK); err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "main", nil
	}
	return repo.DefaultBranch, nil
}

// expect performs a request and fails unless the status matches want.
func (c *Client) expect(ctx context.Context, method, path string, body, out any, want int) (int, error) {
	status, err := c.do(ctx, method, path, body, out)
	if err != nil {
		return status, err
	}
	if status != want {
		return status, fmt.Errorf("%s %s: unexpected status %d", method, path, status)
	}
	return status, nil
}

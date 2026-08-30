// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManifestFile is the manifest's name at the repository root.
const ManifestFile = "replication.json"

// ExcludedFile lists what the gate withheld.
const ExcludedFile = "EXCLUDED.md"

// ErrSecretInContent reports a high-signal secret found inside a file that was
// otherwise cleared for publication. Publishing aborts: the filename gate
// missed it, and shipping it would be worse than a failed publish.
var ErrSecretInContent = errors.New("refusing to publish: secret found in file content")

// Published is the composable result later steps act on — 006c's collaborator
// invitation takes Published.Repo.
type Published struct {
	Repo    Repo
	Preview bool // Live() was not passed; nothing was created
	Existed bool // the repository already existed (idempotent re-publish)
}

// Option configures Publish.
type Option func(*publishConfig)

type publishConfig struct {
	live bool
	now  func() time.Time
}

// Live makes Publish create and push for real. Without it, Publish previews.
func Live() Option { return func(c *publishConfig) { c.live = true } }

// WithClock overrides the source of the manifest's created stamp.
func WithClock(now func() time.Time) Option {
	return func(c *publishConfig) { c.now = now }
}

// Publish carries a plan out against a forge. It previews by default and
// creates nothing; Live() makes it real. When live it creates the private
// repository if absent, then pushes — pushing even when the repository already
// existed, so a run that failed mid-push completes on retry.
func Publish(ctx context.Context, plan Plan, forge Forge, opts ...Option) (Published, error) {
	cfg := publishConfig{now: time.Now}
	for _, o := range opts {
		o(&cfg)
	}

	if !cfg.live {
		return Published{Repo: Repo{Owner: DefaultOrg, Name: plan.RepoName}, Preview: true}, nil
	}

	files, err := assemble(plan, cfg.now())
	if err != nil {
		return Published{}, err
	}

	exists, err := forge.Exists(ctx, plan.RepoName)
	if err != nil {
		return Published{}, fmt.Errorf("checking whether %s exists: %w", plan.RepoName, err)
	}
	repo := Repo{Owner: DefaultOrg, Name: plan.RepoName}
	if !exists {
		if repo, err = forge.Create(ctx, plan.RepoName, true); err != nil {
			return Published{}, fmt.Errorf("creating %s: %w", plan.RepoName, err)
		}
	}
	if err := forge.Push(ctx, repo, files); err != nil {
		return Published{}, fmt.Errorf("pushing to %s: %w", plan.RepoName, err)
	}
	return Published{Repo: repo, Existed: exists}, nil
}

// assemble builds the files to push: the manifest with its created stamp, every
// included file (swept for secrets), and EXCLUDED.md when anything was withheld.
func assemble(plan Plan, now time.Time) ([]File, error) {
	m := plan.Manifest
	m.Reproduction.Created = now.UTC()
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling the manifest: %w", err)
	}
	files := []File{{Path: ManifestFile, Content: append(manifestJSON, '\n')}}

	for _, rel := range plan.Include {
		content, err := os.ReadFile(filepath.Join(plan.SourceDir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", rel, err)
		}
		if reason := secretInContent(content); reason != "" {
			return nil, fmt.Errorf("%w: %s contains %s", ErrSecretInContent, rel, reason)
		}
		files = append(files, File{Path: rel, Content: content})
	}

	if len(plan.Quarantine) > 0 {
		files = append(files, File{Path: ExcludedFile, Content: excludedDoc(plan.Quarantine)})
	}
	return files, nil
}

// secretPatterns are high-signal markers of a leaked credential. This is a
// backstop for a secret under an innocent filename, not a full scanner.
var secretPatterns = []struct{ marker, label string }{
	{"AKIA", "an AWS access key id"},
	{"PRIVATE KEY-----", "a private key block"},
}

func secretInContent(content []byte) string {
	s := string(content)
	for _, p := range secretPatterns {
		if strings.Contains(s, p.marker) {
			return p.label
		}
	}
	return ""
}

func excludedDoc(q []QuarantinedFile) []byte {
	var b strings.Builder
	b.WriteString("# Excluded from this reproduction\n\n")
	b.WriteString("These files were collected during the audit but withheld from publication, ")
	b.WriteString("so the reproduction does not republish content it was not licensed to.\n\n")
	for _, f := range q {
		fmt.Fprintf(&b, "- `%s` — %s\n", f.Path, f.Reason)
	}
	return []byte(b.String())
}

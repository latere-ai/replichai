// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FSForge is a filesystem Forge: each repository is a directory under Root. It
// runs the whole publish loop offline, so it is the default for local use and
// the double every test publishes against. Visibility is recorded in memory so
// a test can assert a reproduction is never created public.
type FSForge struct {
	Root  string
	Owner string

	mu      sync.Mutex
	private map[string]bool
}

// NewFSForge returns a filesystem forge rooted at dir, publishing under owner.
func NewFSForge(dir, owner string) *FSForge {
	return &FSForge{Root: dir, Owner: owner, private: map[string]bool{}}
}

func (f *FSForge) dir(name string) string { return filepath.Join(f.Root, name) }

// Exists reports whether the repository directory is present.
func (f *FSForge) Exists(_ context.Context, name string) (bool, error) {
	info, err := os.Stat(f.dir(name))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s exists but is not a directory", f.dir(name))
	}
	return true, nil
}

// Create makes the repository directory and records its visibility.
func (f *FSForge) Create(_ context.Context, name string, private bool) (Repo, error) {
	if err := os.MkdirAll(f.dir(name), 0o755); err != nil {
		return Repo{}, err
	}
	f.mu.Lock()
	f.private[name] = private
	f.mu.Unlock()
	return f.repo(name), nil
}

// Push writes each file under the repository directory, creating parents.
func (f *FSForge) Push(_ context.Context, repo Repo, files []File) error {
	for _, file := range files {
		dst := filepath.Join(f.dir(repo.Name), filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, file.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (f *FSForge) repo(name string) Repo {
	return Repo{Owner: f.Owner, Name: name, URL: "file://" + f.dir(name)}
}

// IsPrivate reports the visibility recorded for a repository, for tests.
func (f *FSForge) IsPrivate(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.private[name]
}

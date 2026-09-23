// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import "context"

// Repo is a forge's handle to a repository. Publish returns it so a later step,
// such as 006c's collaborator invitation, can act on it with only an owner and
// a name.
type Repo struct {
	Owner string
	Name  string
	URL   string
}

// File is one file to push, its Path relative to the repository root.
type File struct {
	Path    string
	Content []byte
}

// Forge is the interface over repository hosting, in the same style as
// provider.Provider: one adapter per host, replaceable and testable without a
// network. 003 extends it with the read side its registry needs.
type Forge interface {
	// Exists reports whether a repository of this name is already present, so a
	// republish neither duplicates nor recreates it.
	Exists(ctx context.Context, name string) (bool, error)
	// Create makes an empty repository. private must be honored; a
	// reproduction is never created public.
	Create(ctx context.Context, name string, private bool) (Repo, error)
	// Push writes files into the repository, overwriting, so a push that
	// completes a half-finished earlier one converges.
	Push(ctx context.Context, repo Repo, files []File) error
}

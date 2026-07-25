package publish

import "context"

// Repo is a forge's handle to a repository. Publish returns it so a later step —
// 006c's collaborator invitation — can act on it with just an owner and name.
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

// Forge is the seam over repository hosting, mirroring provider.Provider: one
// adapter per host, swappable and testable without a network. 003 extends it
// with the read side its registry needs.
type Forge interface {
	// Exists reports whether a repository of this name is already present, so a
	// republish neither duplicates nor recreates it.
	Exists(ctx context.Context, name string) (bool, error)
	// Create makes an empty repository. private must be honoured — a
	// reproduction is never created public.
	Create(ctx context.Context, name string, private bool) (Repo, error)
	// Push writes files into the repository, overwriting, so a push that
	// completes a half-finished earlier one converges.
	Push(ctx context.Context, repo Repo, files []File) error
}

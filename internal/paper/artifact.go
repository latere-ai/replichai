// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"crypto/sha256"
	"encoding/hex"
)

// The kinds of artifact snapshot.
const (
	// ArtifactGit is a repository, kept as a git bundle of every branch and
	// tag, with the commit its default branch pointed at.
	ArtifactGit = "git"
	// ArtifactFile is one file, kept as it was downloaded.
	ArtifactFile = "file"
)

// Artifact is a snapshot of something a paper's authors released, taken once
// for a study so every run reproduces from the same bytes (046).
type Artifact struct {
	// Source is the address the project listed.
	Source string
	// Kind is ArtifactGit or ArtifactFile.
	Kind string
	// Commit is the commit a git snapshot's default branch pointed at when it
	// was taken. It is empty for a file.
	Commit string
	// Name is the file name a run sees the snapshot under.
	Name    string
	Content []byte
	// SHA256 is the hex digest of Content.
	SHA256 string
}

// SnapshotOf makes an artifact snapshot from bytes already in hand.
func SnapshotOf(source, kind, commit, name string, content []byte) Artifact {
	sum := sha256.Sum256(content)
	return Artifact{
		Source: source, Kind: kind, Commit: commit, Name: name,
		Content: content, SHA256: hex.EncodeToString(sum[:]),
	}
}

// Digest is the snapshot's identity as a run's record writes it,
// "sha256:<hex>".
func (a Artifact) Digest() string { return "sha256:" + a.SHA256 }

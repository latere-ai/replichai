// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/paper"
)

// NetworkProfile names the egress every run of a study is given (046). Without
// one, a run gets whatever its runtime can enforce: open egress where the
// runtime offers it and the artifact allowlist where it does not, so two runs
// of one study on two machines could reach different hosts and find different
// copies of the same artifact. A profile fixes the policy instead, and a
// runtime that cannot enforce it refuses the run rather than giving it
// something else.
type NetworkProfile string

// The profiles. Each is recorded by name in a run's record.
const (
	// ProfileArtifacts reaches the hosts where papers and their released
	// artifacts live, ArtifactDomains, and the model's own endpoint. It is
	// the one profile both local sandboxes can enforce.
	ProfileArtifacts NetworkProfile = "artifacts"
	// ProfileOpen reaches any host. A host sandbox cannot enforce it.
	ProfileOpen NetworkProfile = "open"
)

// DefaultNetworkProfile is the profile a study runs under when it names none.
const DefaultNetworkProfile = ProfileArtifacts

// NetworkProfiles lists every profile, in the order they are documented.
func NetworkProfiles() []NetworkProfile { return []NetworkProfile{ProfileArtifacts, ProfileOpen} }

// ParseNetworkProfile reads a profile's name. An unknown name is an error that
// lists the known ones, because a study that silently ran under another
// profile would record a policy nobody chose.
func ParseNetworkProfile(name string) (NetworkProfile, error) {
	profile := NetworkProfile(strings.ToLower(strings.TrimSpace(name)))
	if slices.Contains(NetworkProfiles(), profile) {
		return profile, nil
	}
	names := make([]string, 0, len(NetworkProfiles()))
	for _, known := range NetworkProfiles() {
		names = append(names, string(known))
	}
	return "", fmt.Errorf("network profile %q is not one of %s", name, strings.Join(names, ", "))
}

// Policy is the egress rule the profile stands for.
func (p NetworkProfile) Policy() managed.NetworkPolicy {
	if p == ProfileOpen {
		return managed.NetworkPolicy{Mode: managed.NetworkOpen}
	}
	return managed.NetworkPolicy{Mode: managed.NetworkAllowlist, AllowedDomains: ArtifactDomains()}
}

// WithNetworkProfile fixes the session's egress to a named profile. A runtime
// that cannot enforce it fails the session before it starts.
func WithNetworkProfile(profile NetworkProfile) SessionOption {
	return func(a *SessionAuditor) { a.profile = profile }
}

// WithPaperCopy hands the session a copy of the paper the system fetched, read
// only and outside the reproduction, so every run of a study reads the same
// text (046). A runtime that cannot mount an input read-only fails the session
// before it starts.
func WithPaperCopy(copied paper.Copy) SessionOption {
	return func(a *SessionAuditor) { a.paper = &copied }
}

// WithArtifacts hands the session the study's snapshots of what the paper's
// authors released, read-only and outside the reproduction, so every run of a
// study reproduces from the same materials (046). A runtime that cannot mount
// an input read-only fails the session before it starts.
func WithArtifacts(snapshots []paper.Artifact) SessionOption {
	return func(a *SessionAuditor) { a.artifacts = slices.Clone(snapshots) }
}

// network is the session's egress: the profile's policy where one is named,
// refused if the runtime cannot enforce it, and otherwise whatever the runtime
// can enforce.
func (a *SessionAuditor) network(capabilities managed.Capabilities) (managed.NetworkPolicy, error) {
	if a.profile == "" {
		return networkFor(capabilities), nil
	}
	policy := a.profile.Policy()
	if !capabilities.NetworkModes[policy.Mode] {
		return managed.NetworkPolicy{}, fmt.Errorf("%w: the %s network profile needs %s egress, which this runtime cannot enforce",
			managed.ErrUnsupported, a.profile, policy.Mode)
	}
	return policy, nil
}

// paperInput is the resource that carries the paper copy, and the path the
// session reads it at.
func (a *SessionAuditor) paperInput(capabilities managed.Capabilities) (managed.Resource, string, error) {
	if capabilities.InputDir == "" {
		return managed.Resource{}, "", fmt.Errorf("%w: this runtime cannot hand a run a read-only copy of the paper", managed.ErrUnsupported)
	}
	name := paperFileName(*a.paper)
	resource := managed.Resource{Kind: managed.ResourceInput, Input: &managed.InputResource{
		Path: name, MediaType: a.paper.MediaType, Content: a.paper.Content, SHA256: a.paper.SHA256,
	}}
	return resource, filepath.Join(capabilities.InputDir, name), nil
}

// paperFileName is the name the copy is given in the session: paper.pdf for a
// PDF, and paper with the source's extension otherwise, so a reader of the
// instructions knows what kind of file to open.
func paperFileName(copied paper.Copy) string {
	if copied.MediaType == "application/pdf" {
		return "paper.pdf"
	}
	source := copied.Source
	// An address's extension is its path's, never its query's.
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" {
		source = parsed.Path
	}
	extension := strings.ToLower(filepath.Ext(source))
	if extension == "" || len(extension) > 8 {
		return "paper"
	}
	return "paper" + extension
}

// paperProtocol tells the session where its copy of the paper is. The method
// says to read a copy the run was given in place of fetching one, and this is
// where the run learns it was given one.
func paperProtocol(path string) string {
	return fmt.Sprintf(`

## The paper you were given

The system fetched this paper's full text once for this study and gave every run of it the same copy. It is at %[1]s, and it is read-only. That file is the paper: read all of it, and do not fetch the paper's text from anywhere else, because a run that reads another version may compare its results against different reported values. In SOURCES.md, say that the text you audited is the copy you were given. The copy is not part of the reproduction: do not copy it into the repository. You still fetch the paper's landing pages, its supplementary material and anything else you need when you hunt for artifacts.`, path)
}

// artifactsDir is where a session's artifact snapshots sit under its inputs.
const artifactsDir = "artifacts"

// handedArtifact is one snapshot as the session is given it: the snapshot and
// the path the session reads it at.
type handedArtifact struct {
	paper.Artifact
	path string
}

// artifactInputs are the resources that carry the study's artifact snapshots,
// and the snapshots with the paths the session reads them at. Two snapshots
// with one name are told apart by their place in the list, so neither is
// written over the other.
func (a *SessionAuditor) artifactInputs(capabilities managed.Capabilities) ([]managed.Resource, []handedArtifact, error) {
	if capabilities.InputDir == "" {
		return nil, nil, fmt.Errorf("%w: this runtime cannot hand a run read-only snapshots of what the authors released", managed.ErrUnsupported)
	}
	taken := map[string]bool{}
	resources := make([]managed.Resource, 0, len(a.artifacts))
	handed := make([]handedArtifact, 0, len(a.artifacts))
	for index, snapshot := range a.artifacts {
		name := snapshot.Name
		if name == "" || taken[name] {
			name = fmt.Sprintf("%d-%s", index+1, cmpOr(snapshot.Name, "artifact"))
		}
		taken[name] = true
		relative := artifactsDir + "/" + name
		resources = append(resources, managed.Resource{Kind: managed.ResourceInput, Input: &managed.InputResource{
			Path: relative, MediaType: "application/octet-stream", Content: snapshot.Content, SHA256: snapshot.SHA256,
		}})
		handed = append(handed, handedArtifact{Artifact: snapshot, path: filepath.Join(capabilities.InputDir, filepath.FromSlash(relative))})
	}
	return resources, handed, nil
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// artifactsProtocol tells the session which of the authors' released materials
// it was handed, and how to use them. They are the materials the study
// reproduces the paper from; the hunt for anything else the authors released
// goes on as the method describes.
func artifactsProtocol(handed []handedArtifact) string {
	var b strings.Builder
	b.WriteString(`

## The released materials you were given

The study snapshotted these materials the authors released, once, and gave every run of it the same snapshots, read-only:
`)
	for _, artifact := range handed {
		switch artifact.Kind {
		case paper.ArtifactGit:
			name := strings.TrimSuffix(filepath.Base(artifact.path), filepath.Ext(artifact.path))
			fmt.Fprintf(&b, "\n- %s: the repository, as a git bundle at %s, with its default branch at commit %s. "+
				"Clone it with `git clone %s \"$TMPDIR/%s\"` and work from the clone.",
				artifact.Source, artifact.path, artifact.Commit, artifact.path, name)
		default:
			fmt.Fprintf(&b, "\n- %s: the file as it was downloaded, at %s.", artifact.Source, artifact.path)
		}
	}
	b.WriteString(`

Reproduce from these snapshots rather than fetching the same materials again, so every run of the study works from the same ones. Record each in the "released" list of verdict.json under its source address. Keep anything you unpack from them outside the repository, as you would any third-party file. You may still look for anything else the authors released.`)
	return b.String()
}

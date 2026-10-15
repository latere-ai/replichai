// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
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

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package host is the default sandbox driver: the harness already installed on
// the machine, confined by srt — Seatbelt on macOS, bubblewrap with seccomp on
// Linux — to one workspace and an allow-only network (045b).
//
// It starts in milliseconds, needs no image and no virtual machine, and reuses
// the harness's own login rather than demanding a raw API key. What it does not
// give is a pinned toolchain; that is the container driver, and the environment
// record is what makes an unpinned run honest.
package host

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Settings is srt's configuration file. The field names are srt's own, so this
// struct is the one place a change in that contract has to be tracked.
type Settings struct {
	Network    NetworkSettings    `json:"network"`
	Filesystem FilesystemSettings `json:"filesystem"`
}

// NetworkSettings is the egress policy. srt denies everything by default and
// allows named domains through a host proxy, which is what lets this driver
// declare the allowlist mode honestly.
type NetworkSettings struct {
	AllowedDomains []string `json:"allowedDomains"`
	DeniedDomains  []string `json:"deniedDomains"`
	// TLSTerminate carries the domains left uninspected. srt stands in the
	// middle of egress with its own authority to inspect it, which a client
	// that does not read the injected trust store refuses outright.
	TLSTerminate *TLSSettings `json:"tlsTerminate,omitempty"`
	// AllowLocalBinding stays false: a reproduction that binds a local
	// port is talking to something outside the sandbox's account.
	AllowLocalBinding bool `json:"allowLocalBinding"`
	// AllowAllUnixSockets stays false. A unix socket is a hole straight
	// through the boundary to whatever service is listening.
	AllowAllUnixSockets bool `json:"allowAllUnixSockets"`
}

// TLSSettings names what egress is not inspected.
type TLSSettings struct {
	ExcludeDomains []string `json:"excludeDomains"`
}

// FilesystemSettings is the path policy. srt reads allowRead as an override of
// denyRead, and denyWrite as an override of allowWrite, so the two halves are
// written in opposite directions on purpose.
type FilesystemSettings struct {
	DenyRead   []string `json:"denyRead"`
	AllowRead  []string `json:"allowRead"`
	AllowWrite []string `json:"allowWrite"`
	DenyWrite  []string `json:"denyWrite"`
}

// AlwaysDenyRead is the credential list this driver refuses to read whatever
// else is allowed. It is a table with a test behind it rather than a comment,
// because a default that widens by accident is how a rule like this fails.
//
// Entries are expanded against the operator's home directory.
var AlwaysDenyRead = []string{
	"~/.ssh",
	"~/.aws",
	"~/.config/gcloud",
	"~/.kube",
	"~/.netrc",
	"~/.gnupg",
	"~/.docker/config.json",
	"~/.git-credentials",
	"~/.config/replichai",
	"~/.npmrc",
	"~/.pypirc",
}

// Profile is what one stage may touch, before it is rendered into srt's file.
type Profile struct {
	// Home is the operator's home directory, denied wholesale so that only
	// what is named below is readable inside it.
	Home string
	// Reads are the paths the stage may read: its own session, the
	// harness's configuration, and where the harness is installed.
	Reads []string
	// Writes are the paths it may write.
	Writes []string
	// Denies are paths withheld from a read grant that would otherwise
	// cover them: srt keeps a denial more specific than its allowance,
	// which is how one file is withheld from a directory that has to be
	// readable for another.
	Denies []string
	// DenyWrites are paths inside a writable tree that stay read-only. The
	// evidence directory is the one that matters: srt's allow-only write
	// pattern would otherwise let a writable parent carry it.
	DenyWrites []string
	// Network is the egress policy.
	Network sandbox.Network
}

// Render turns a profile into srt's settings file.
//
// There is no open mode. srt is allow-only and refuses a wildcard outright —
// "Overly broad patterns like *.com or * are not allowed" — so a session that
// asks for unrestricted egress cannot be expressed here at all. The driver
// declares that rather than emitting a profile srt would reject, which is what
// it did until a real run met it.
//
// The two halves run in opposite directions because srt's own precedence does:
// denyRead is broad and allowRead cuts holes in it, so the whole home directory
// is denied and the harness's own configuration is allowed back; allowWrite is
// narrow and denyWrite cuts holes in it, so only the session's writable
// directories are listed and the evidence directory is denied inside them.
func Render(profile Profile) Settings {
	settings := Settings{
		Filesystem: FilesystemSettings{
			DenyRead:   expandAll(profile.Home, append(append([]string{"~"}, AlwaysDenyRead...), profile.Denies...)),
			AllowRead:  unique(profile.Reads),
			AllowWrite: unique(profile.Writes),
			DenyWrite:  unique(profile.DenyWrites),
		},
	}
	switch profile.Network.Mode {
	case sandbox.NetworkAllowlist:
		settings.Network.AllowedDomains = unique(profile.Network.Domains)
		// Always declared, even with nothing to exclude. srt publishes its
		// certificate authority to the standard per-tool trust variables
		// only when termination is configured, and without them every
		// HTTPS client in the sandbox rejects the proxy's certificate —
		// which looks like a network failure rather than a trust one.
		settings.Network.TLSTerminate = &TLSSettings{
			ExcludeDomains: unique(profile.Network.Passthrough),
		}
	default:
		settings.Network.AllowedDomains = []string{}
	}
	settings.Network.DeniedDomains = []string{}
	return settings
}

// Encode renders the settings as the file srt reads.
func (s Settings) Encode() ([]byte, error) {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sandbox settings: %w", err)
	}
	return append(body, '\n'), nil
}

// expandAll resolves a leading ~ against home. A path that cannot be expanded
// is kept as written rather than dropped: a deny entry that silently vanishes
// is worse than one that matches nothing.
func expandAll(home string, paths []string) []string {
	expanded := make([]string, 0, len(paths))
	for _, path := range paths {
		expanded = append(expanded, expand(home, path))
	}
	return unique(expanded)
}

func expand(home, path string) string {
	if home == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

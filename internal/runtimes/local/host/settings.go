// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package host is the default sandbox driver. It runs the harness already
// installed on the machine, confined by srt to one workspace and an allow-only
// network (045b). srt uses Seatbelt on macOS and bubblewrap with seccomp on
// Linux.
//
// The driver starts in milliseconds, needs no image and no virtual machine,
// and reuses the harness's own login rather than requiring a raw API key. It
// does not provide a pinned toolchain; the container driver does. The
// environment record is what makes an unpinned run reproducible.
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
// struct is the only place where a change in that format has to be tracked.
type Settings struct {
	Network    NetworkSettings    `json:"network"`
	Filesystem FilesystemSettings `json:"filesystem"`
}

// NetworkSettings is the egress policy. srt denies everything by default and
// allows named domains through a host proxy, which is why this driver can
// declare the allowlist mode.
type NetworkSettings struct {
	AllowedDomains []string `json:"allowedDomains"`
	DeniedDomains  []string `json:"deniedDomains"`
	// TLSTerminate names the domains whose traffic is not inspected. srt
	// intercepts egress with its own certificate authority in order to
	// inspect it, and a client that does not read the injected trust store
	// rejects the connection.
	TLSTerminate *TLSSettings `json:"tlsTerminate,omitempty"`
	// AllowLocalBinding stays false. A reproduction that binds a local port
	// is communicating with something outside the sandbox's control.
	AllowLocalBinding bool `json:"allowLocalBinding"`
	// AllowAllUnixSockets stays false. A unix socket bypasses the boundary
	// and reaches whatever service is listening on it.
	AllowAllUnixSockets bool `json:"allowAllUnixSockets"`
}

// TLSSettings names the domains whose egress is not inspected.
type TLSSettings struct {
	ExcludeDomains []string `json:"excludeDomains"`
}

// FilesystemSettings is the path policy. srt treats allowRead as an override
// of denyRead, and denyWrite as an override of allowWrite, so the read and
// write halves are deliberately expressed in opposite directions.
type FilesystemSettings struct {
	DenyRead   []string `json:"denyRead"`
	AllowRead  []string `json:"allowRead"`
	AllowWrite []string `json:"allowWrite"`
	DenyWrite  []string `json:"denyWrite"`
}

// AlwaysDenyRead lists the credential paths this driver denies reading
// regardless of what else is allowed. It is a table checked by a test rather
// than a comment, because a rule like this fails when a default widens by
// accident.
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
	"~/.env",
}

// Profile describes what one stage may access, before it is rendered into
// srt's settings file.
type Profile struct {
	// Home is the operator's home directory. The whole directory is denied
	// so that only the paths named below are readable inside it.
	Home string
	// Reads are the paths the stage may read: its own session, the
	// harness's configuration, and the harness's install directory.
	Reads []string
	// Writes are the paths the stage may write.
	Writes []string
	// Denies are paths withheld from a read grant that would otherwise
	// cover them. srt honours a denial more specific than its allowance,
	// which is how one file is withheld from a directory that must be
	// readable for another file.
	Denies []string
	// DenyWrites are paths inside a writable tree that stay read-only. The
	// evidence directory is the important one: srt's allow-only write
	// pattern would otherwise let a writable parent include it.
	DenyWrites []string
	// Network is the egress policy.
	Network sandbox.Network
}

// Render turns a profile into srt's settings file.
//
// There is no open mode. srt is allow-only and refuses a wildcard with the
// message "Overly broad patterns like *.com or * are not allowed", so a
// session that asks for unrestricted egress cannot be expressed here. The
// driver declares this in its capabilities rather than emitting a profile srt
// would reject, which is what it did until a real run encountered it.
//
// The read and write halves run in opposite directions because srt's
// precedence does. denyRead is broad and allowRead makes exceptions to it, so
// the whole home directory is denied and the harness's own configuration is
// allowed back. allowWrite is narrow and denyWrite makes exceptions to it, so
// only the session's writable directories are listed and the evidence
// directory is denied inside them.
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
		// TLSTerminate is always declared, even with nothing to exclude. srt
		// publishes its certificate authority to the standard per-tool trust
		// variables only when termination is configured. Without them every
		// HTTPS client in the sandbox rejects the proxy's certificate, which
		// presents as a network failure rather than a trust failure.
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
// is kept as written rather than dropped, because a deny entry that silently
// disappears is worse than one that matches nothing.
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

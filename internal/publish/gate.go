// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"path"
	"strings"

	"github.com/latere-ai/replichai/internal/method"
)

// Origin says who produced an artifact. The zero value is deliberately unknown,
// so an unclassified file is never mistaken for worker-written.
type Origin uint8

// The origins an artifact can have.
const (
	OriginUnknown    Origin = iota
	OriginWorker            // a fixed report file, or anything under src/
	OriginThirdParty        // everything else — downloaded, not written here
)

// Provenance is what the gate knows about one artifact: its path and origin.
type Provenance struct {
	Path   string
	Origin Origin
}

// Classify is the redistribution decision, pure and reusable: an artifact is
// published only when it is worker-written; anything else is withheld with a
// reason. This is the fail-closed gate — 005's transcript gate reuses it.
func Classify(p Provenance) (include bool, reason string) {
	if p.Origin == OriginWorker {
		return true, ""
	}
	return false, "not written by the reproduction worker"
}

// workerReports are the fixed top-level files the method asks the worker to
// write, taken from the method itself so the gate cannot hold a stale copy of
// the list. Everything under method.SourceDir is worker-written too; everything
// else is third-party.
var workerReports = func() map[string]bool {
	m := make(map[string]bool)
	for _, name := range method.Deliverables() {
		m[name] = true
	}
	return m
}()

// originOf classifies an artifact path structurally, with no I/O.
func originOf(p string) Origin {
	clean := path.Clean(strings.TrimPrefix(p, "./"))
	if workerReports[clean] {
		return OriginWorker
	}
	if clean == method.SourceDir || strings.HasPrefix(clean, method.SourceDir+"/") {
		return OriginWorker
	}
	return OriginThirdParty
}

// secretNames and secretExts are filenames that must never be published. This
// is a filename check, not a proof of secret-freedom. Content is swept
// separately, and only where the system chose neither the file nor its
// contents: RedactSecrets runs over the transcript before it is committed. A
// key pasted into a worker-written file is caught by neither, and the control
// there is that a stage is given only the credentials it needs.
var (
	secretNames = map[string]bool{
		"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
		".netrc": true, ".npmrc": true, ".pypirc": true,
	}
	secretExts = []string{".pem", ".key", ".pfx", ".p12", ".keystore"}
)

// isSecretName reports whether a path's base name is a known credential file.
func isSecretName(p string) bool {
	base := strings.ToLower(path.Base(p))
	if secretNames[base] || strings.HasPrefix(base, ".env") {
		return true
	}
	for _, ext := range secretExts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

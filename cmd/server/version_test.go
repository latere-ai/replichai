// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// TestPrintVersion holds the line the image gate reads: the service name,
// then the version the build reports.
func TestPrintVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v1.2.3"

	var b strings.Builder
	if err := printVersion(&b); err != nil {
		t.Fatalf("printVersion: %v", err)
	}
	if got, want := b.String(), "replichai v1.2.3\n"; got != want {
		t.Errorf("printVersion = %q, want %q", got, want)
	}
}

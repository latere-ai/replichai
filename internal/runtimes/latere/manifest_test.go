// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package latere

import "testing"

// An agent is named by a digest of its spec. The name below was derived with
// the encoder this package used before, so agents already applied under it are
// found again rather than duplicated.
func TestAgentNameIsStableForASpecWithMarkupCharacters(t *testing.T) {
	manifest, err := workerManifest("a <b> & c", "img", "m", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "replichai-f50c452255f66764"; manifest.Metadata.Name != want {
		t.Errorf("agent name = %q, want %q", manifest.Metadata.Name, want)
	}
}

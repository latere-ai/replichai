// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import "testing"

func TestKeyNameRefusesEscapes(t *testing.T) {
	store, err := NewFSArtifactStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  string
		want string
		ok   bool
	}{
		{"a/b.txt", "a/b.txt", true},
		{"a/./b.txt", "a/b.txt", true},
		{"a/../b.txt", "b.txt", true},
		{"..hidden", "..hidden", true},
		{"", "", false},
		{".", "", false},
		{"..", "", false},
		{"../x", "", false},
		{"a/../../x", "", false},
		{"/abs", "", false},
		{"a\x00b", "", false},
	}
	for _, c := range cases {
		got, err := store.keyName(c.key)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("keyName(%q) = %q, %v; want %q, ok=%v", c.key, got, err, c.want, c.ok)
		}
	}
}

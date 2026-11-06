// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package managed

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
	"time"
)

// A raw value is left out only when it is absent. An object that happens to be
// empty, and a JSON null, are recorded as written.
func TestRawValuesAreOmittedOnlyWhenAbsent(t *testing.T) {
	at := time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC)
	cases := []struct {
		name string
		raw  jsontext.Value
		want string
	}{
		{"absent", nil, `{"id":"e","role":"agent","kind":"message","time":"2026-11-05T01:02:03Z"}`},
		{"empty object", jsontext.Value(`{}`), `{"id":"e","role":"agent","kind":"message","time":"2026-11-05T01:02:03Z","raw":{}}`},
		{"null", jsontext.Value(`null`), `{"id":"e","role":"agent","kind":"message","time":"2026-11-05T01:02:03Z","raw":null}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(Event{ID: "e", Role: RoleAgent, Kind: "message", Time: at, Raw: c.raw})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestSessionRefLocatorIsOmittedOnlyWhenAbsent(t *testing.T) {
	got, err := json.Marshal(SessionRef{Runtime: "r", ID: "s", Revision: 2, LocatorVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"runtime":"r","id":"s","revision":2,"locator_version":1}`; string(got) != want {
		t.Errorf("absent: got %s, want %s", got, want)
	}
	got, err = json.Marshal(SessionRef{Runtime: "r", ID: "s", Revision: 2, LocatorVersion: 1, Locator: jsontext.Value(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"runtime":"r","id":"s","revision":2,"locator_version":1,"locator":{}}`; string(got) != want {
		t.Errorf("empty object: got %s, want %s", got, want)
	}
}

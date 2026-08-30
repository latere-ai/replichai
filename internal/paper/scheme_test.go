// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"encoding/json"
	"testing"
)

func TestSchemeMarshalsToStableName(t *testing.T) {
	cases := map[Scheme]string{
		SchemeDOI:    `"doi"`,
		SchemeArXiv:  `"arxiv"`,
		SchemeSHA256: `"sha256"`,
	}
	for scheme, want := range cases {
		got, err := json.Marshal(scheme)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", scheme, err)
		}
		if string(got) != want {
			t.Errorf("Marshal(%v) = %s, want %s", scheme, got, want)
		}
		var back Scheme
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", got, err)
		}
		if back != scheme {
			t.Errorf("round-trip %s = %v, want %v", got, back, scheme)
		}
	}
}

func TestSchemeUnknownIsNotValid(t *testing.T) {
	if SchemeUnknown.Valid() {
		t.Error("the zero value must not read as a real scheme")
	}
	if !SchemeDOI.Valid() {
		t.Error("SchemeDOI should be valid")
	}
}

func TestSchemeUnmarshalRejectsUnknown(t *testing.T) {
	var s Scheme
	if err := json.Unmarshal([]byte(`"isbn"`), &s); err == nil {
		t.Error("unmarshalling an unknown scheme should fail")
	}
}

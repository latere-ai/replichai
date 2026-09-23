// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"errors"
	"testing"
)

// TestValidate covers each optional field: absent is valid, present and
// malformed is refused by its own error, so the HTTP layer can name the field.
func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
		want error
	}{
		{"empty is valid", Profile{}, nil},
		{"name and institution are free text", Profile{DisplayName: "Ada", Institution: "LMU"}, nil},
		{"plain contact", Profile{Contact: "ada@example.org"}, nil},
		{"contact with a display name is refused", Profile{Contact: "Ada <ada@example.org>"}, ErrInvalidContact},
		{"contact list is refused", Profile{Contact: "a@x.org, b@x.org"}, ErrInvalidContact},
		{"contact without a domain", Profile{Contact: "ada"}, ErrInvalidContact},
		{"orcid", Profile{ORCID: "0000-0002-1825-0097"}, nil},
		{"orcid with an X checksum", Profile{ORCID: "0000-0002-1694-233X"}, nil},
		{"orcid too short", Profile{ORCID: "0000-0002-1825"}, ErrInvalidORCID},
		{"orcid with a url", Profile{ORCID: "https://orcid.org/0000-0002-1825-0097"}, ErrInvalidORCID},
		{"scholar", Profile{Scholar: "https://scholar.google.com/citations?user=abc"}, nil},
		{"scholar on a country domain", Profile{Scholar: "https://scholar.google.de/citations?user=abc"}, nil},
		{"scholar elsewhere is refused", Profile{Scholar: "https://example.com/me"}, ErrInvalidScholar},
		{"scholar over http is refused", Profile{Scholar: "http://scholar.google.com/citations?user=abc"}, ErrInvalidScholar},
		{"a lookalike host is refused", Profile{Scholar: "https://scholar.google.com.evil.test/x"}, ErrInvalidScholar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.Normalize().Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate(%+v) = %v, want %v", tc.p, err, tc.want)
			}
		})
	}
}

// TestRedactEmail checks the redaction behind the published signer line: the
// address is recognizable to its owner and unusable to a harvester, and a
// value that is not an address never passes through unmasked.
func TestRedactEmail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// A one- or two-character local part is masked whole: showing the first
		// character of "hi" would reveal half the address.
		{"hi@changkun.de", "**@changkun.de"},
		{"ada.lovelace@example.org", "a**********e@example.org"},
		{"  ada@example.org  ", "a*a@example.org"},
		{"ab@example.org", "**@example.org"},
		{"a@example.org", "*@example.org"},
		{"not-an-email", ""},
		{"@example.org", ""},
		{"ada@", ""},
		{"", ""},
	} {
		if got := RedactEmail(tc.in); got != tc.want {
			t.Errorf("RedactEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"encoding/json"
	"fmt"
)

// Scheme is the kind of external identifier a paper's identity resolves to.
//
// The zero value is SchemeUnknown, so a struct field that was never set cannot
// pass as a real scheme, and an identity is never keyed on an unset one.
type Scheme uint8

// The identifier schemes, in the priority order resolution tries them.
const (
	SchemeUnknown Scheme = iota
	SchemeDOI
	SchemeArXiv
	SchemeSHA256
)

var schemeNames = map[Scheme]string{
	SchemeUnknown: "unknown",
	SchemeDOI:     "doi",
	SchemeArXiv:   "arxiv",
	SchemeSHA256:  "sha256",
}

// Valid reports whether s is a scheme this package defines and can key on.
func (s Scheme) Valid() bool { _, ok := schemeNames[s]; return ok && s != SchemeUnknown }

// String returns the value's stable name.
func (s Scheme) String() string {
	if name, ok := schemeNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Scheme(%d)", uint8(s))
}

// MarshalJSON encodes the value as its stable name.
func (s Scheme) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON decodes a stable name, rejecting anything unrecognised.
func (s *Scheme) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for scheme, n := range schemeNames {
		if n == name {
			*s = scheme
			return nil
		}
	}
	return fmt.Errorf("unknown scheme %q", name)
}

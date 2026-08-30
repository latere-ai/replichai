// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"crypto/rand"
	"strings"
)

// suffixLen is how many base-32 characters a repository suffix carries. Four
// gives ~1M values; 003 handles the rare collision by regenerating.
const suffixLen = 4

// suffixAlphabet is a 32-symbol lowercase set with the ambiguous letters
// (i, l, o, u) left out. Exactly 32 symbols, so a byte maps to one with no
// modulo bias (256 = 8 × 32).
const suffixAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// NewSuffix returns suffixLen lowercase base-32 characters from a cryptographic
// source. 003 wraps this with a collision check against the registry;
// standalone, one draw is enough.
func NewSuffix() (string, error) { return NewSuffixN(suffixLen) }

// NewSuffixN returns n lowercase base-32 characters. 003 widens from four to
// six after repeated collisions.
func NewSuffixN(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.Grow(n)
	for _, c := range b {
		sb.WriteByte(suffixAlphabet[int(c)%len(suffixAlphabet)])
	}
	return sb.String(), nil
}

// repoName joins a slug and suffix into "<slug>-<suffix>".
func repoName(slug, suffix string) string { return slug + "-" + suffix }

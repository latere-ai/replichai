// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import "strings"

// maxSlugLen is the character budget for a slug. A title longer than this is
// filled word by word up to the last word that fits.
const maxSlugLen = 48

// leadingArticles are dropped from the front of a title before slugging, so
// "The Foo" and "Foo" slug alike. Only leading articles are dropped; an
// article in the middle of a title is a real word.
var leadingArticles = map[string]bool{"the": true, "a": true, "an": true}

// Slug derives a presentation slug from a title: ASCII, lowercase,
// hyphen-separated, no leading or trailing hyphen, at most maxSlugLen
// characters on a word boundary. It returns "" when the title folds to nothing
// (all CJK, symbols, or empty); the caller substitutes the identity key.
func Slug(title string) string {
	words := slugWords(title)
	for len(words) > 0 && leadingArticles[words[0]] {
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}

	var b strings.Builder
	for _, w := range words {
		next := len(w)
		if b.Len() > 0 {
			next += 1 + b.Len() // the joining hyphen plus what is already there
		}
		if next > maxSlugLen {
			if b.Len() == 0 {
				// A single first word longer than the budget: take a hard
				// prefix so an over-long title still yields a slug.
				return w[:maxSlugLen]
			}
			break
		}
		if b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteString(w)
	}
	return b.String()
}

// slugWords folds a title to lowercase ASCII and splits it into alphanumeric
// words, dropping every character that is not a letter or digit.
func slugWords(title string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range title {
		for _, f := range foldRune(r) {
			switch {
			case f >= 'a' && f <= 'z', f >= '0' && f <= '9':
				cur.WriteRune(f)
			case f >= 'A' && f <= 'Z':
				cur.WriteRune(f + ('a' - 'A'))
			default:
				flush()
			}
		}
	}
	flush()
	return words
}

// foldRune reduces a rune to ASCII: ASCII passes through, common Latin accents
// decompose to their base letters, and anything with no ASCII equivalent (CJK,
// mathematical symbols, emoji) folds to nothing so it drops out of the slug.
func foldRune(r rune) []rune {
	if r < 0x80 {
		return []rune{r}
	}
	if s, ok := latinFold[r]; ok {
		return []rune(s)
	}
	return nil
}

// latinFold maps accented Latin letters and a few ligatures to ASCII. It is not
// a full Unicode decomposition — it covers what appears in paper titles, and
// anything outside it folds away, which the key-fallback makes safe.
var latinFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'ç': "c", 'ć': "c", 'č': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i",
	'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u",
	'ý': "y", 'ÿ': "y",
	'ž': "z", 'ź': "z", 'ż': "z",
	'š': "s", 'ś': "s", 'ş': "s",
	'ł': "l",
	'ß': "ss", 'æ': "ae", 'œ': "oe",
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A",
	'Ç': "C", 'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E",
	'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I", 'Ñ': "N",
	'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", 'Ø': "O",
	'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U", 'Ý': "Y",
	'Ž': "Z", 'Š': "S", 'Æ': "AE", 'Œ': "OE",
}

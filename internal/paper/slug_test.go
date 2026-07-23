package paper

import (
	"strings"
	"testing"
)

// pleasQLarifyTitle is the arXiv/publisher title of the first paper audited.
const pleasQLarifyTitle = "PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying"

// TestSlugGreedyFillTo48 pins the truncation rule and proves the Decisions
// example: the title is longer than the 48-character budget, so the slug stops
// on the last whole word that fits.
func TestSlugGreedyFillTo48(t *testing.T) {
	got := Slug(pleasQLarifyTitle)
	const want = "pleasqlarify-visual-pragmatic-repair-for-natural"
	if got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
	if len(got) > maxSlugLen {
		t.Errorf("slug is %d characters, over the %d budget", len(got), maxSlugLen)
	}
}

// TestSlugShapeInvariants covers AC4 across a range of titles.
func TestSlugShapeInvariants(t *testing.T) {
	titles := []string{
		pleasQLarifyTitle,
		"A Short One",
		"Café Culture and Naïve Bayes",
		"Über den Wolken: Ein Ansatz",
		"   Leading and trailing   ",
		"punctuation!!! ...everywhere???",
	}
	for _, title := range titles {
		s := Slug(title)
		if s == "" {
			continue // empty is a valid fold-to-nothing result, covered elsewhere
		}
		if s != strings.ToLower(s) {
			t.Errorf("Slug(%q) = %q is not lowercase", title, s)
		}
		if len(s) > maxSlugLen {
			t.Errorf("Slug(%q) = %q exceeds %d characters", title, s, maxSlugLen)
		}
		if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
			t.Errorf("Slug(%q) = %q has a leading or trailing hyphen", title, s)
		}
		for _, r := range s {
			ok := r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
			if !ok {
				t.Errorf("Slug(%q) = %q contains non-ASCII-slug rune %q", title, s, r)
			}
		}
	}
}

// TestSlugFoldsUnicodeToASCII covers AC5's non-empty case: accents fold to
// their base letters rather than dropping the word.
func TestSlugFoldsUnicodeToASCII(t *testing.T) {
	got := Slug("Café Culture and Naïve Bayes")
	const want = "cafe-culture-and-naive-bayes"
	if got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
}

// TestSlugFoldsToNothing covers AC5's fallback trigger: a title that is all
// CJK or symbols folds to the empty string, and the caller substitutes the key.
func TestSlugFoldsToNothing(t *testing.T) {
	for _, title := range []string{"深度学习方法", "∑ ∇ ∂ ∞", ""} {
		if got := Slug(title); got != "" {
			t.Errorf("Slug(%q) = %q, want empty so the key can take over", title, got)
		}
	}
}

func TestSlugDropsLeadingArticle(t *testing.T) {
	if got := Slug("The Quick Brown Fox"); got != "quick-brown-fox" {
		t.Errorf("Slug = %q, want %q", got, "quick-brown-fox")
	}
	// An article in the middle is a real word, not dropped.
	if got := Slug("Fox and the Hound"); got != "fox-and-the-hound" {
		t.Errorf("Slug = %q, want %q", got, "fox-and-the-hound")
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// pages is a fetcher over fixed answers. An address it does not hold fails, as
// a refused or missing page would.
type pages map[string]string

func (p pages) Fetch(_ context.Context, address string) ([]byte, error) {
	body, ok := p[address]
	if !ok {
		return nil, errors.New("403 Forbidden")
	}
	return []byte(body), nil
}

const aPDF = "%PDF-1.7 the paper"

func TestFullTextFetchesArXivsOwnPDF(t *testing.T) {
	copied, err := FullText(t.Context(), "arXiv:2605.19151v2", pages{"https://arxiv.org/pdf/2605.19151": aPDF})
	if err != nil {
		t.Fatalf("FullText: %v", err)
	}
	sum := sha256.Sum256([]byte(aPDF))
	if string(copied.Content) != aPDF || copied.MediaType != "application/pdf" ||
		copied.SHA256 != hex.EncodeToString(sum[:]) || copied.Digest() != "sha256:"+copied.SHA256 ||
		copied.Source != "https://arxiv.org/pdf/2605.19151" {
		t.Fatalf("copy = %+v", copied)
	}
}

// A landing page is not the paper. The PDF it declares is.
func TestFullTextFollowsTheLandingPagesDeclaredPDF(t *testing.T) {
	landing := `<html><meta name="citation_pdf_url" content="/doi/pdf/10.1145/3654777.3676417?download=true&amp;v=2"></html>`
	copied, err := FullText(t.Context(), "10.1145/3654777.3676417", pages{
		"https://doi.org/10.1145/3654777.3676417":                           landing,
		"https://doi.org/doi/pdf/10.1145/3654777.3676417?download=true&v=2": aPDF,
	})
	if err != nil || copied.Source != "https://doi.org/doi/pdf/10.1145/3654777.3676417?download=true&v=2" {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
}

func TestFullTextTakesAURLThatIsAPDF(t *testing.T) {
	copied, err := FullText(t.Context(), "https://example.org/paper.pdf", pages{"https://example.org/paper.pdf": aPDF})
	if err != nil || copied.Source != "https://example.org/paper.pdf" {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
}

// A publisher that refuses automated clients is gone around through the
// open-access copies OpenAlex lists, the best one first.
func TestFullTextFallsBackToAnOpenAccessCopy(t *testing.T) {
	copied, err := FullText(t.Context(), "doi:10.1145/2702123.2702520", pages{
		openAlexAPI + "10.1145/2702123.2702520": `{"best_oa_location":{"pdf_url":"https://author.example/kay.pdf"},
			"locations":[{"pdf_url":"https://author.example/kay.pdf"},{"pdf_url":"https://mirror.example/kay.pdf"},{"pdf_url":null}]}`,
		"https://author.example/kay.pdf": aPDF,
	})
	if err != nil || copied.Source != "https://author.example/kay.pdf" {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
}

// A page that is not a PDF and names none, a declared link that is not a PDF,
// and an OpenAlex record with no copy all fail, and the error says what was
// tried so the operator knows to supply a file.
func TestFullTextSaysWhatItTriedWhenNothingIsAPDF(t *testing.T) {
	for name, test := range map[string]struct {
		reference string
		pages     pages
		want      []string
	}{
		"sign-in page": {"10.1145/3654777.3676417", pages{
			"https://doi.org/10.1145/3654777.3676417": "<html>Sign in to read</html>",
			openAlexAPI + "10.1145/3654777.3676417":   `{"locations":[]}`,
		}, []string{"not a PDF, and the page names none", "no open-access copy"}},
		"declared link is a page": {"https://example.org/abs/1", pages{
			"https://example.org/abs/1": `<meta name="citation_pdf_url" content="https://example.org/pdf/1">`,
			"https://example.org/pdf/1": "<html>Sign in</html>",
		}, []string{"https://example.org/pdf/1: not a PDF"}},
		"declared link refused": {"https://example.org/abs/1", pages{
			"https://example.org/abs/1": `<meta name="citation_pdf_url" content="https://example.org/pdf/1">`,
		}, []string{"https://example.org/pdf/1: 403"}},
		"OpenAlex unreadable": {"10.1145/3654777.3676417", pages{openAlexAPI + "10.1145/3654777.3676417": "{"}, []string{"OpenAlex:"}},
		"OpenAlex refused":    {"10.1145/3654777.3676417", pages{}, []string{"OpenAlex: 403"}},
		"nothing to look up":  {"a title, not a reference", pages{}, []string{"names no arXiv id, DOI or address"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FullText(t.Context(), test.reference, test.pages)
			if !errors.Is(err, ErrNoFullText) {
				t.Fatalf("err = %v, want ErrNoFullText", err)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to say %q", err, want)
				}
			}
		})
	}
	if _, err := FullText(t.Context(), "10.1145/3654777.3676417", nil); !errors.Is(err, ErrNoFullText) {
		t.Fatalf("no fetcher = %v", err)
	}
}

func TestCopyOfReadsTheMediaTypeFromTheBytes(t *testing.T) {
	if got := CopyOf([]byte(aPDF), "paper.pdf").MediaType; got != "application/pdf" {
		t.Errorf("a PDF reads as %q", got)
	}
	if got := CopyOf([]byte("plain text"), "paper.pdf").MediaType; got != "application/octet-stream" {
		t.Errorf("text named .pdf reads as %q", got)
	}
}

func TestResolveAgainstKeepsALinkItCannotRead(t *testing.T) {
	if got := resolveAgainst("://bad", "/pdf"); got != "/pdf" {
		t.Errorf("resolveAgainst = %q", got)
	}
	if got := resolveAgainst("https://example.org/a", "%zz"); got != "%zz" {
		t.Errorf("resolveAgainst = %q", got)
	}
}

func TestASnapshotIsIdentifiedByItsBytes(t *testing.T) {
	snapshot := SnapshotOf("https://github.com/authors/system", ArtifactGit, "abc123", "system.bundle", []byte("bundle"))
	sum := sha256.Sum256([]byte("bundle"))
	if snapshot.SHA256 != hex.EncodeToString(sum[:]) || snapshot.Digest() != "sha256:"+snapshot.SHA256 ||
		snapshot.Kind != ArtifactGit || snapshot.Commit != "abc123" || snapshot.Name != "system.bundle" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

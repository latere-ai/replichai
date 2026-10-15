// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strings"
)

// ErrNoFullText reports a reference whose full text could not be fetched from
// any place this package knows to look. A publisher that refuses automated
// clients is the usual cause, and the remedy is a copy the operator supplies.
var ErrNoFullText = errors.New("no copy of the paper could be fetched")

// openAlexAPI is where a DOI's open-access copies are listed. It answers for
// papers whose publisher pages refuse automated clients, and names a copy an
// author or a repository holds.
const openAlexAPI = "https://api.openalex.org/works/doi:"

// pdfMagic is how every PDF file begins.
var pdfMagic = []byte("%PDF-")

// citationPDFRe is the page's own declaration of where its paper's PDF is, in
// the convention academic publishers and preprint servers share.
var citationPDFRe = metaRe(`citation_pdf_url`)

// Copy is one copy of a paper's full text: the bytes, where they came from,
// and their digest, which a run's record cites as the paper it was given.
type Copy struct {
	Content   []byte
	MediaType string
	// Source is the address the copy was fetched from, or the file it was
	// read from.
	Source string
	// SHA256 is the hex digest of Content.
	SHA256 string
}

// Digest is the copy's identity as a run's record writes it,
// "sha256:<hex>".
func (c Copy) Digest() string { return "sha256:" + c.SHA256 }

// CopyOf makes a copy from bytes already in hand, such as a file the operator
// supplied. The media type is read from the bytes, not from a file name.
func CopyOf(content []byte, source string) Copy {
	sum := sha256.Sum256(content)
	mediaType := "application/octet-stream"
	if bytes.HasPrefix(content, pdfMagic) {
		mediaType = "application/pdf"
	}
	return Copy{Content: content, MediaType: mediaType, Source: source, SHA256: hex.EncodeToString(sum[:])}
}

// FullText fetches a PDF of the paper reference names, so that every run of a
// study can be handed the same copy rather than each finding its own (046).
//
// The places tried, in order: arXiv's own PDF for an arXiv reference; the
// address itself where the reference is a URL that serves a PDF; the PDF the
// landing page of the URL or the DOI declares; and, for a DOI, the open-access
// copies OpenAlex lists. Only a body that is a PDF is accepted. A page that
// says the paper is behind a sign-in is not the paper, and handing a run a
// copy of that page would misstate what every run read.
//
// When nothing yields a PDF the error wraps ErrNoFullText and says what was
// tried.
func FullText(ctx context.Context, reference string, fetcher Fetcher) (Copy, error) {
	reference = strings.TrimSpace(reference)
	if fetcher == nil {
		return Copy{}, fmt.Errorf("%w: %s: no fetcher", ErrNoFullText, reference)
	}
	var tried []string
	attempt := func(address string) (Copy, bool) {
		body, err := fetcher.Fetch(ctx, address)
		switch {
		case err != nil:
			tried = append(tried, fmt.Sprintf("%s: %v", address, err))
		case bytes.HasPrefix(body, pdfMagic):
			return CopyOf(body, address), true
		default:
			// A landing page declares where its PDF is. The declaration is
			// followed once; a PDF link that leads to another page is not a
			// copy.
			declared := html.UnescapeString(metaContent(body, citationPDFRe))
			if declared == "" {
				tried = append(tried, address+": not a PDF, and the page names none")
				return Copy{}, false
			}
			target := resolveAgainst(address, declared)
			linked, err := fetcher.Fetch(ctx, target)
			switch {
			case err != nil:
				tried = append(tried, fmt.Sprintf("%s: %v", target, err))
			case bytes.HasPrefix(linked, pdfMagic):
				return CopyOf(linked, target), true
			default:
				tried = append(tried, target+": not a PDF")
			}
		}
		return Copy{}, false
	}

	var candidates []string
	if id, ok := extractArXiv(reference); ok {
		candidates = append(candidates, "https://arxiv.org/pdf/"+id)
	}
	if looksLikeURL(reference) {
		candidates = append(candidates, reference)
	}
	doi, hasDOI := extractDOI(reference)
	if hasDOI && !looksLikeURL(reference) {
		candidates = append(candidates, "https://doi.org/"+doi)
	}
	for _, candidate := range candidates {
		if copied, ok := attempt(candidate); ok {
			return copied, nil
		}
	}
	if hasDOI {
		for _, location := range openAccessCopies(ctx, fetcher, doi, &tried) {
			if copied, ok := attempt(location); ok {
				return copied, nil
			}
		}
	}
	if len(tried) == 0 {
		return Copy{}, fmt.Errorf("%w: %s names no arXiv id, DOI or address", ErrNoFullText, reference)
	}
	return Copy{}, fmt.Errorf("%w: %s; tried %s", ErrNoFullText, reference, strings.Join(tried, "; "))
}

// openAccessCopies lists the PDFs OpenAlex knows of for a DOI, the best one
// first. A lookup that fails is noted among what was tried and lists nothing.
func openAccessCopies(ctx context.Context, fetcher Fetcher, doi string, tried *[]string) []string {
	body, err := fetcher.Fetch(ctx, openAlexAPI+doi)
	if err != nil {
		*tried = append(*tried, fmt.Sprintf("OpenAlex: %v", err))
		return nil
	}
	type location struct {
		PDF string `json:"pdf_url"`
	}
	var work struct {
		Best      *location  `json:"best_oa_location"`
		Locations []location `json:"locations"`
	}
	if err := json.Unmarshal(body, &work); err != nil {
		*tried = append(*tried, fmt.Sprintf("OpenAlex: %v", err))
		return nil
	}
	var out []string
	add := func(address string) {
		if address != "" && !slices.Contains(out, address) {
			out = append(out, address)
		}
	}
	if work.Best != nil {
		add(work.Best.PDF)
	}
	for _, location := range work.Locations {
		add(location.PDF)
	}
	if len(out) == 0 {
		*tried = append(*tried, "OpenAlex: no open-access copy listed")
	}
	return out
}

// resolveAgainst reads a link a page declared relative to the page's address.
// A link that does not parse is returned as written, and its fetch reports why.
func resolveAgainst(page, link string) string {
	base, err := url.Parse(page)
	if err != nil {
		return link
	}
	target, err := base.Parse(link)
	if err != nil {
		return link
	}
	return target.String()
}

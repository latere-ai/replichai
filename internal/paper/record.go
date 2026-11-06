// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"encoding/json/v2"
	"regexp"
	"strconv"
	"strings"

	"github.com/latere-ai/replichai/internal/wirejson"
)

// Record holds the citation data for a paper: its authors, venue, and date. It
// is returned with the Identity, and a project stores it beside the paper so a
// screen can render a reference without another network request (037).
//
// Every field comes from the identifier's metadata service or from the
// publisher's own declaration. Nothing is inferred, and nothing is written by
// a model. A citation that is plausible but wrong is worse than one that is
// absent, because a reader cannot tell it is wrong. A field no source declared
// stays empty and is shown as missing.
type Record struct {
	// Authors in the order the source gave them. Order is part of a citation.
	Authors []string `json:"authors,omitempty"`
	// Venue is the journal or the proceedings: Crossref's container-title, an
	// arXiv journal reference, or a page's citation_journal_title.
	Venue string `json:"venue,omitempty"`
	// Year the paper was published, or 0 when no source declares one.
	Year int `json:"year,omitzero"`
	// Volume, Issue and Pages are kept as the source spells them, including a
	// page range such as "1128-1145", because a citation reproduces them rather
	// than parsing them.
	Volume string `json:"volume,omitempty"`
	Issue  string `json:"issue,omitempty"`
	Pages  string `json:"pages,omitempty"`
	// Publisher, where the source names one. Crossref does; arXiv does not.
	Publisher string `json:"publisher,omitempty"`
	// Abstract is the source's abstract with its markup removed and nothing
	// else changed. Many records have none, since Crossref carries one only
	// when the publisher deposited it. A paper without one shows none rather
	// than a summary written for it.
	Abstract string `json:"abstract,omitempty"`
}

// Empty reports whether nothing is known about the paper beyond its
// identifier. A bare URL with no citation tags yields an empty record, and the
// project screen shows one as a paper still needing its details.
func (r Record) Empty() bool {
	return len(r.Authors) == 0 && r.Venue == "" && r.Year == 0 &&
		r.Volume == "" && r.Issue == "" && r.Pages == "" && r.Publisher == "" && r.Abstract == ""
}

// crossrefRecord is the part of a Crossref work this package reads. Crossref
// returns the whole work regardless, so reading these fields costs parsing
// only, not an extra request.
type crossrefRecord struct {
	Message struct {
		Title  []string `json:"title"`
		Author []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
			Name   string `json:"name"`
		} `json:"author"`
		ContainerTitle []string `json:"container-title"`
		Issued         struct {
			DateParts [][]int `json:"date-parts"`
		} `json:"issued"`
		PublishedPrint struct {
			DateParts [][]int `json:"date-parts"`
		} `json:"published-print"`
		Volume    string `json:"volume"`
		Issue     string `json:"issue"`
		Page      string `json:"page"`
		Publisher string `json:"publisher"`
		Abstract  string `json:"abstract"`
	} `json:"message"`
}

// parseCrossref reads a Crossref work into a title and a record. A malformed or
// empty response yields zero values, which every caller treats as nothing to
// add rather than as a failure. Enrichment is best-effort and must never fail
// a resolution that already has its key.
func parseCrossref(body []byte) (string, Record) {
	var w crossrefRecord
	if json.Unmarshal(body, &w, wirejson.Foreign) != nil {
		return "", Record{}
	}
	m := w.Message
	rec := Record{
		Volume:    plainText(m.Volume),
		Issue:     plainText(m.Issue),
		Pages:     plainText(m.Page),
		Publisher: plainText(m.Publisher),
		Abstract:  plainAbstract(m.Abstract),
	}
	for _, a := range m.Author {
		// A person has given and family names; an organization has one `name`.
		// Both are rendered the way the source would print them.
		if n := plainText(a.Given + " " + a.Family); n != "" {
			rec.Authors = append(rec.Authors, n)
		} else if n := plainText(a.Name); n != "" {
			rec.Authors = append(rec.Authors, n)
		}
	}
	if len(m.ContainerTitle) > 0 {
		rec.Venue = plainText(m.ContainerTitle[0])
	}
	// issued is the date the DOI's content was first made available. A work
	// issued online in one year and printed in another keeps the earlier year,
	// which is the year a citation to the DOI refers to.
	rec.Year = firstYear(m.Issued.DateParts, m.PublishedPrint.DateParts)
	title := ""
	if len(m.Title) > 0 {
		title = plainText(m.Title[0])
	}
	return title, rec
}

// firstYear returns the first year in the first of the date-part lists that has
// one. Crossref's date-parts is [[year, month, day]], with month and day
// optional; a work dated to a year alone is [[2024]].
func firstYear(lists ...[][]int) int {
	for _, parts := range lists {
		if len(parts) > 0 && len(parts[0]) > 0 && parts[0][0] > 0 {
			return parts[0][0]
		}
	}
	return 0
}

// metaRe builds the matcher for one Highwire / Google Scholar meta tag, the
// per-page declaration academic publishers emit about their own paper.
//
// The content is matched up to its own closing delimiter rather than up to the
// first quote of either kind. A conference is called CHI '24 and a title often
// contains a possessive, and reading up to the first apostrophe inside a
// double-quoted attribute truncated both. Group 1 is a double-quoted value,
// group 2 a single-quoted one, and exactly one of them matches.
func metaRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)<meta[^>]+name=["']` + name + `["'][^>]*content=(?:"([^"]*)"|'([^']*)')`)
}

// The meta tags this package reads. They are the page's own declaration of
// its paper's bibliography, in a convention every academic publisher and
// indexer shares, not a scrape of the body text.
var (
	citationAuthorRe    = metaRe(`citation_author`)
	citationJournalRe   = metaRe(`citation_journal_title`)
	citationConfRe      = metaRe(`citation_conference_title`)
	citationDateRe      = metaRe(`citation_(?:publication_)?date`)
	citationYearRe      = metaRe(`citation_year`)
	citationVolumeRe    = metaRe(`citation_volume`)
	citationIssueRe     = metaRe(`citation_issue`)
	citationFirstPageRe = metaRe(`citation_firstpage`)
	citationLastPageRe  = metaRe(`citation_lastpage`)
	citationPublisherRe = metaRe(`citation_publisher`)
	citationAbstractRe  = metaRe(`citation_abstract`)
	// Crossref deposits an abstract as JATS XML, and a publisher page may emit
	// one with inline markup. The tags are removed and the words are left as
	// the source wrote them.
	tagRe  = regexp.MustCompile(`<[^>]*>`)
	yearRe = regexp.MustCompile(`(\d{4})`)
)

// recordFromBody reads a page's own declaration of its paper's bibliography.
// A page that declares none yields an empty record, as a bare link to a PDF
// does.
//
// Author names are kept as the page spells them, including the "Family, Given"
// form. The convention is not universal enough to invert safely, and a name is
// the field where a transformation is most likely to be wrong about a real
// person's name. The owner can correct it, and the correction is permanent.
func recordFromBody(body []byte) Record {
	rec := Record{
		Authors:   metaContents(body, citationAuthorRe),
		Abstract:  plainAbstract(metaContent(body, citationAbstractRe)),
		Venue:     firstNonEmpty(metaContent(body, citationJournalRe), metaContent(body, citationConfRe)),
		Volume:    metaContent(body, citationVolumeRe),
		Issue:     metaContent(body, citationIssueRe),
		Publisher: metaContent(body, citationPublisherRe),
		Year:      yearFrom(firstNonEmpty(metaContent(body, citationDateRe), metaContent(body, citationYearRe))),
		Pages:     pageRange(metaContent(body, citationFirstPageRe), metaContent(body, citationLastPageRe)),
	}
	return rec
}

// metaContents returns the content of every meta tag matched by re, in document
// order. It exists for authors: a page emits one citation_author per author,
// and their order is the author order.
func metaContents(body []byte, re *regexp.Regexp) []string {
	var out []string
	for _, m := range re.FindAllSubmatch(body, -1) {
		if s := plainText(captured(m)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// captured returns whichever of the two delimiter groups matched.
func captured(m [][]byte) string {
	for _, g := range m[1:] {
		if len(g) > 0 {
			return string(g)
		}
	}
	return ""
}

// yearFrom pulls a four-digit year out of a date a source spells its own way:
// "2024", "2024-05-01", "2024/05/01", "May 2024".
func yearFrom(s string) int {
	m := yearRe.FindString(s)
	if m == "" {
		return 0
	}
	y, err := strconv.Atoi(m)
	if err != nil {
		return 0
	}
	return y
}

// pageRange joins a first and last page the way a citation prints them, and
// keeps a single page single.
func pageRange(first, last string) string {
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	switch {
	case first == "":
		return ""
	case last == "" || last == first:
		return first
	default:
		return first + "-" + last
	}
}

// plainAbstract turns a source's abstract into plain text: markup removed,
// whitespace collapsed, and the redundant "Abstract" heading many publishers
// deposit as the first word dropped.
//
// It is the only transformation this package makes to a field's content. It
// changes rendering rather than meaning, because a tag such as <jats:p> is not
// part of what the authors wrote.
func plainAbstract(s string) string {
	s = plainText(tagRe.ReplaceAllString(s, " "))
	for _, lead := range []string{"Abstract ", "ABSTRACT ", "Summary "} {
		if after, ok := strings.CutPrefix(s, lead); ok {
			s = strings.TrimSpace(after)
			break
		}
	}
	return s
}

// mergeRecord fills the empty fields of first from second, and never
// overwrites a field first already declared. The argument order is the order
// of trust: Crossref before a publisher's page, a DOI's record before the
// preprint's. A correction by the owner relies on the same rule, that an
// existing value wins; here it is applied between two sources rather than
// between a person and a source.
func mergeRecord(first, second Record) Record {
	out := first
	if len(out.Authors) == 0 {
		out.Authors = second.Authors
	}
	out.Venue = firstNonEmpty(out.Venue, second.Venue)
	out.Volume = firstNonEmpty(out.Volume, second.Volume)
	out.Issue = firstNonEmpty(out.Issue, second.Issue)
	out.Pages = firstNonEmpty(out.Pages, second.Pages)
	out.Publisher = firstNonEmpty(out.Publisher, second.Publisher)
	out.Abstract = firstNonEmpty(out.Abstract, second.Abstract)
	if out.Year == 0 {
		out.Year = second.Year
	}
	return out
}

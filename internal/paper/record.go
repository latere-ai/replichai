package paper

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Record is what a citation is made of: who wrote the paper, where it appeared,
// and when. It travels with the Identity, and a project stores it beside the
// paper so a screen can render a reference without asking the network again
// (037).
//
// Every field comes from the identifier's own metadata service or from the
// publisher's own declaration of it. Nothing here is inferred, and nothing here
// is written by a model: a citation that is plausible rather than correct is
// worse than one that is absent, because nothing about it looks wrong. A field
// no source declared stays empty and is shown as missing.
type Record struct {
	// Authors in the order the source gave them. Order is part of a citation.
	Authors []string `json:"authors,omitempty"`
	// Venue is the journal or the proceedings — Crossref's container-title, an
	// arXiv journal reference, a page's citation_journal_title.
	Venue string `json:"venue,omitempty"`
	// Year the paper was published, or 0 when no source declares one.
	Year int `json:"year,omitempty"`
	// Volume, Issue and Pages as the source spells them, page range included
	// ("1128-1145"), because a citation reproduces them rather than parsing them.
	Volume string `json:"volume,omitempty"`
	Issue  string `json:"issue,omitempty"`
	Pages  string `json:"pages,omitempty"`
	// Publisher, where the source names one. Crossref does; arXiv does not.
	Publisher string `json:"publisher,omitempty"`
}

// Empty reports whether nothing at all is known about the paper beyond its
// identifier — which is what a bare URL with no citation tags yields, and what
// the project screen shows as a paper still needing its details.
func (r Record) Empty() bool {
	return len(r.Authors) == 0 && r.Venue == "" && r.Year == 0 &&
		r.Volume == "" && r.Issue == "" && r.Pages == "" && r.Publisher == ""
}

// crossrefRecord is the part of a Crossref record this reads. Crossref returns the
// whole work either way — the cost of these fields is parsing, not a request.
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
	} `json:"message"`
}

// parseCrossref reads a Crossref work into a title and a record. A malformed or
// empty response yields both zero, which every caller treats as "nothing to
// add" rather than as a failure: enrichment is best-effort and must never fail a
// resolution that already has its key.
func parseCrossref(body []byte) (string, Record) {
	var w crossrefRecord
	if json.Unmarshal(body, &w) != nil {
		return "", Record{}
	}
	m := w.Message
	rec := Record{
		Volume:    strings.TrimSpace(m.Volume),
		Issue:     strings.TrimSpace(m.Issue),
		Pages:     strings.TrimSpace(m.Page),
		Publisher: collapseWS(m.Publisher),
	}
	for _, a := range m.Author {
		// A person has given and family names; an organisation has one `name`.
		// Both are rendered the way the source would print them.
		if n := collapseWS(strings.TrimSpace(a.Given + " " + a.Family)); n != "" {
			rec.Authors = append(rec.Authors, n)
		} else if n := collapseWS(a.Name); n != "" {
			rec.Authors = append(rec.Authors, n)
		}
	}
	if len(m.ContainerTitle) > 0 {
		rec.Venue = collapseWS(m.ContainerTitle[0])
	}
	// `issued` is when the DOI's content was first made available; a work that
	// was issued online in one year and printed in another keeps the earlier,
	// which is what a citation to the DOI means.
	rec.Year = firstYear(m.Issued.DateParts, m.PublishedPrint.DateParts)
	title := ""
	if len(m.Title) > 0 {
		title = collapseWS(m.Title[0])
	}
	return title, rec
}

// firstYear returns the first year in the first of the date-part lists that has
// one. Crossref's date-parts is [[year, month, day]], with month and day
// optional — a work dated to a year alone is [[2024]].
func firstYear(lists ...[][]int) int {
	for _, parts := range lists {
		if len(parts) > 0 && len(parts[0]) > 0 && parts[0][0] > 0 {
			return parts[0][0]
		}
	}
	return 0
}

// metaRe builds the matcher for one Highwire / Google Scholar meta tag — the
// per-page declaration academic publishers emit about their own paper.
//
// The content is matched by its own delimiter rather than by "not a quote of
// either kind": a conference is called CHI '24 and a title contains a possessive
// often enough that reading up to the first apostrophe inside a double-quoted
// attribute silently truncated both. Group 1 is a double-quoted value, group 2 a
// single-quoted one, and exactly one of them matches.
func metaRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)<meta[^>]+name=["']` + name + `["'][^>]*content=(?:"([^"]*)"|'([^']*)')`)
}

// The tags this reads. A declaration, not a scrape: the page is stating its
// paper's own bibliography in a convention every academic publisher and indexer
// shares.
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
	yearRe              = regexp.MustCompile(`(\d{4})`)
)

// recordFromBody reads a page's own declaration of its paper's bibliography.
// A page that declares none yields an empty record, which is exactly what a
// bare link to a PDF does.
//
// Author names are kept as the page spells them, "Family, Given" included. The
// convention is not universal enough to invert safely, and a name is the field
// where a clever transformation is most likely to be wrong about somebody's
// actual name — the owner can correct it, and their correction is permanent.
func recordFromBody(body []byte) Record {
	rec := Record{
		Authors:   metaContents(body, citationAuthorRe),
		Venue:     firstNonEmpty(metaContent(body, citationJournalRe), metaContent(body, citationConfRe)),
		Volume:    metaContent(body, citationVolumeRe),
		Issue:     metaContent(body, citationIssueRe),
		Publisher: metaContent(body, citationPublisherRe),
	}
	rec.Year = yearFrom(firstNonEmpty(metaContent(body, citationDateRe), metaContent(body, citationYearRe)))
	rec.Pages = pageRange(metaContent(body, citationFirstPageRe), metaContent(body, citationLastPageRe))
	return rec
}

// metaContents returns the content of every meta tag matched by re, in document
// order. Authors are the reason it exists: a page emits one citation_author per
// author, and their order is the authorship.
func metaContents(body []byte, re *regexp.Regexp) []string {
	var out []string
	for _, m := range re.FindAllSubmatch(body, -1) {
		if s := collapseWS(captured(m)); s != "" {
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

// mergeRecord fills the gaps in a record from a second source, and never
// overwrites what the first already declared. The order of the arguments is the
// order of trust: Crossref before a publisher's page, a DOI's record before the
// preprint's. It is the same rule a correction relies on — what is already
// there wins — applied between two sources rather than between a person and a
// source.
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
	if out.Year == 0 {
		out.Year = second.Year
	}
	return out
}

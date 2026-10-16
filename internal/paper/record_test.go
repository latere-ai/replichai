// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// crossrefFull is a full Crossref work of the shape the real service returns:
// authors with given and family parts, a container title, a date-parts issue
// date, and the volume, issue, page and publisher fields, which an earlier
// version fetched and discarded.
const crossrefFull = `{"message":{
  "title":["Effects of Semantic Segmentation Visualization on Trust"],
  "author":[
    {"given":"Ada","family":"Lovelace"},
    {"given":"Grace","family":"Hopper"},
    {"name":"The Working Group"}
  ],
  "container-title":["Proceedings of the ACM on Human-Computer Interaction"],
  "issued":{"date-parts":[[2024,5,11]]},
  "volume":"8",
  "issue":"CSCW1",
  "page":"1128-1145",
  "publisher":"Association for Computing Machinery"
}}`

// AC1: a DOI comes back with the record Crossref publishes for it, authors in
// the order the source gave them.
func TestResolveDOIKeepsTheCrossrefRecord(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{"api.crossref.org": []byte(crossrefFull)}}
	id, err := Resolve(context.Background(), theDOI, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := Record{
		Authors:   []string{"Ada Lovelace", "Grace Hopper", "The Working Group"},
		Venue:     "Proceedings of the ACM on Human-Computer Interaction",
		Year:      2024,
		Volume:    "8",
		Issue:     "CSCW1",
		Pages:     "1128-1145",
		Publisher: "Association for Computing Machinery",
	}
	if !reflect.DeepEqual(id.Record, want) {
		t.Errorf("record =\n%+v\nwant\n%+v", id.Record, want)
	}
}

// Crossref and publisher pages carry HTML entities in plain-text fields. A
// venue read as "Computers &amp; Graphics" was shown to readers verbatim.
func TestEntitiesInMetadataAreDecoded(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{"api.crossref.org": []byte(`{"message":{
	  "title":["Context &amp; Location"],
	  "author":[{"given":"Hans-W","family":"Gellersen"},{"name":"R&amp;D Group"}],
	  "container-title":["Computers &amp; Graphics"],
	  "publisher":"Elsevier &amp; Co",
	  "issued":{"date-parts":[[1999]]}}}`)}}
	id, err := Resolve(context.Background(), theDOI, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Title != "Context & Location" || id.Record.Venue != "Computers & Graphics" || id.Record.Publisher != "Elsevier & Co" {
		t.Errorf("title=%q venue=%q publisher=%q, want the entities decoded", id.Title, id.Record.Venue, id.Record.Publisher)
	}
	if want := []string{"Hans-W Gellersen", "R&D Group"}; !reflect.DeepEqual(id.Record.Authors, want) {
		t.Errorf("authors = %q, want %q", id.Record.Authors, want)
	}

	page := []byte(`<html><head>
  <meta name="citation_author" content="O&#39;Brien, Pat">
  <meta name="citation_journal_title" content="Computers &amp; Graphics">
</head></html>`)
	fetch = &fakeFetcher{responses: map[string][]byte{"example.edu": page}}
	id, err = Resolve(context.Background(), "https://example.edu/papers/two", WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Record.Venue != "Computers & Graphics" || len(id.Record.Authors) != 1 || id.Record.Authors[0] != "O'Brien, Pat" {
		t.Errorf("page record = %+v, want the entities decoded", id.Record)
	}
}

// AC1, the sparse case: a record that declares fewer fields resolves anyway,
// and the undeclared fields are left empty rather than invented.
func TestResolveDOIWithASparseCrossrefRecord(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"api.crossref.org": []byte(`{"message":{"title":["A Paper"],"issued":{"date-parts":[[2019]]}}}`),
	}}
	id, err := Resolve(context.Background(), theDOI, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Title != "A Paper" || id.Record.Year != 2019 {
		t.Fatalf("title=%q year=%d, want the two fields the record has", id.Title, id.Record.Year)
	}
	if len(id.Record.Authors) != 0 || id.Record.Venue != "" || id.Record.Pages != "" {
		t.Errorf("undeclared fields were filled in: %+v", id.Record)
	}
	if id.Record.Empty() {
		t.Error("a record with a year is not empty")
	}
}

// AC2: an arXiv id yields the preprint's authors and year, and its journal
// reference when the preprint carries one.
func TestResolveArXivKeepsAuthorsAndYear(t *testing.T) {
	feed := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <title>Retention Under Load</title>
    <published>2023-01-09T18:00:00Z</published>
    <arxiv:journal_ref>CHI '24, 1-14</arxiv:journal_ref>
    <author><name>Ada Lovelace</name></author>
    <author><name>Grace Hopper</name></author>
  </entry>
</feed>`)
	fetch := &fakeFetcher{responses: map[string][]byte{"export.arxiv.org": feed}}
	id, err := Resolve(context.Background(), "2603.01795", WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := id.Record.Authors; !reflect.DeepEqual(got, []string{"Ada Lovelace", "Grace Hopper"}) {
		t.Errorf("authors = %v", got)
	}
	if id.Record.Year != 2023 {
		t.Errorf("year = %d, want 2023 from <published>", id.Record.Year)
	}
	if id.Record.Venue != "CHI '24, 1-14" {
		t.Errorf("venue = %q, want the journal reference", id.Record.Venue)
	}
}

// TestArXivWithADOIPrefersCrossrefAndKeepsTheRest checks that a preprint
// naming a DOI resolves to that DOI, that Crossref is then the preferred
// source for its record, and that the preprint still supplies what Crossref
// omits.
func TestArXivWithADOIPrefersCrossrefAndKeepsTheRest(t *testing.T) {
	feed := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <title>Preprint Title</title>
    <arxiv:doi>` + theDOI + `</arxiv:doi>
    <published>2023-01-09T18:00:00Z</published>
    <author><name>Ada Lovelace</name></author>
  </entry>
</feed>`)
	fetch := &fakeFetcher{responses: map[string][]byte{
		"export.arxiv.org": feed,
		// The published record names the venue and the year; it has no authors.
		"api.crossref.org": []byte(`{"message":{"title":["Published Title"],"container-title":["CHI"],"issued":{"date-parts":[[2024]]}}}`),
	}}
	id, err := Resolve(context.Background(), "2603.01795", WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Title != "Published Title" {
		t.Errorf("title = %q, want the published one", id.Title)
	}
	if id.Record.Year != 2024 || id.Record.Venue != "CHI" {
		t.Errorf("record = %+v, want the published year and venue", id.Record)
	}
	if !reflect.DeepEqual(id.Record.Authors, []string{"Ada Lovelace"}) {
		t.Errorf("authors = %v, want the preprint's, which Crossref did not carry", id.Record.Authors)
	}
}

// AC3: a publisher page declares its own bibliography in citation_* meta tags,
// one per author, and that is what a URL with no identifier resolves to.
func TestResolveURLReadsTheCitationTags(t *testing.T) {
	page := []byte(`<html><head>
  <meta name="citation_title" content="A Study of Something">
  <meta name="citation_author" content="Lovelace, Ada">
  <meta name="citation_author" content="Hopper, Grace">
  <meta name="citation_journal_title" content="Interacting with Computers">
  <meta name="citation_publication_date" content="2022/03/14">
  <meta name="citation_volume" content="34">
  <meta name="citation_issue" content="2">
  <meta name="citation_firstpage" content="201">
  <meta name="citation_lastpage" content="219">
  <meta name="citation_publisher" content="Oxford University Press">
</head><body>a paper</body></html>`)
	fetch := &fakeFetcher{responses: map[string][]byte{"example.edu": page}}
	id, err := Resolve(context.Background(), "https://example.edu/papers/one", WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := Record{
		// Kept as the page spells them. Inverting a name is a guess about a
		// real person's name, and the owner can correct it.
		Authors:   []string{"Lovelace, Ada", "Hopper, Grace"},
		Venue:     "Interacting with Computers",
		Year:      2022,
		Volume:    "34",
		Issue:     "2",
		Pages:     "201-219",
		Publisher: "Oxford University Press",
	}
	if !reflect.DeepEqual(id.Record, want) {
		t.Errorf("record =\n%+v\nwant\n%+v", id.Record, want)
	}
}

// AC3, the empty case: a page that declares nothing about itself resolves to a
// paper with no record, rather than to a paper with an invented one.
func TestResolveURLWithNoDeclarationIsSparse(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"example.edu": []byte(`<html><head><title>Some PDF</title></head><body>x</body></html>`),
	}}
	id, err := Resolve(context.Background(), "https://example.edu/one.pdf", WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !id.Record.Empty() {
		t.Errorf("record = %+v, want empty for a page that declares nothing", id.Record)
	}
}

// TestConferencePageAndSinglePage checks that a conference page names its
// venue with citation_conference_title, and that a page with one page number
// cites a single page rather than a range.
func TestConferencePageAndSinglePage(t *testing.T) {
	page := []byte(`<html><head>
  <meta name="citation_title" content="One Page">
  <meta name="citation_conference_title" content="CHI '24">
  <meta name="citation_firstpage" content="7">
  <meta name="citation_lastpage" content="7">
</head></html>`)
	rec := recordFromBody(page)
	if rec.Venue != "CHI '24" {
		t.Errorf("venue = %q, want the conference title", rec.Venue)
	}
	if rec.Pages != "7" {
		t.Errorf("pages = %q, want a single page", rec.Pages)
	}
}

// TestDOIFallsBackToThePage checks that the DOI path falls back to the
// publisher's page for what Crossref does not answer, including a DOI Crossref
// has no record of.
func TestDOIFallsBackToThePage(t *testing.T) {
	page := []byte(`<html><head>
  <meta name="citation_title" content="From The Page">
  <meta name="citation_author" content="Ada Lovelace">
  <meta name="citation_journal_title" content="A Journal">
</head></html>`)
	fetch := &fakeFetcher{responses: map[string][]byte{
		"api.crossref.org":  []byte(`{"message":{}}`),
		"publisher.example": page,
	}}
	id, err := Resolve(context.Background(), "https://publisher.example/doi/"+theDOI, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Title != "From The Page" || id.Record.Venue != "A Journal" {
		t.Errorf("title=%q record=%+v, want the page's declaration", id.Title, id.Record)
	}
}

// TestABrokenSourceStillResolves checks that enrichment is best-effort: a
// metadata service that is down or returns malformed data must not fail a
// resolution that already has its key.
func TestABrokenSourceStillResolves(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{"api.crossref.org": []byte(`not json at all`)}}
	id, err := Resolve(context.Background(), theDOI, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Key != theDOI || !id.Record.Empty() {
		t.Errorf("id = %+v, want the key with an empty record", id)
	}
	if id.Slug == "" {
		t.Error("a resolution with no title still needs its key-based slug")
	}
}

func TestMergeRecordNeverOverwrites(t *testing.T) {
	first := Record{Authors: []string{"Ada Lovelace"}, Venue: "CHI", Year: 2024}
	second := Record{Authors: []string{"Somebody Else"}, Venue: "Elsewhere", Year: 1999, Pages: "1-9", Publisher: "ACM"}
	got := mergeRecord(first, second)
	if !reflect.DeepEqual(got.Authors, first.Authors) || got.Venue != "CHI" || got.Year != 2024 {
		t.Errorf("merge overwrote the trusted source: %+v", got)
	}
	if got.Pages != "1-9" || got.Publisher != "ACM" {
		t.Errorf("merge did not fill the gaps: %+v", got)
	}
}

func TestYearFromTheWaysSourcesSpellADate(t *testing.T) {
	for in, want := range map[string]int{
		"2024":                 2024,
		"2024-05-11":           2024,
		"2024/05/11":           2024,
		"May 2024":             2024,
		"2023-01-09T18:00:00Z": 2023,
		"":                     0,
		"no date here":         0,
	} {
		if got := yearFrom(in); got != want {
			t.Errorf("yearFrom(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestNothingInferred checks that no record field is read from the paper's
// prose. Nothing in this package may write a record from a model or from body
// text; every field is set from a parsed declaration.
func TestNothingInferred(t *testing.T) {
	rec := recordFromBody([]byte(`<html><body>Ada Lovelace wrote this in 1843 for the Journal of Everything.</body></html>`))
	if !rec.Empty() {
		t.Errorf("a record was read out of prose rather than a declaration: %+v", rec)
	}
	if strings.Contains(strings.Join(rec.Authors, ""), "Ada") {
		t.Error("an author was guessed from the body text")
	}
}

// TestAnApostropheInsideAQuotedAttribute covers a truncation bug: a conference
// is called CHI '24, and reading a double-quoted attribute up to the first
// apostrophe inside it lost the year. The same matcher reads the title and the
// DOI, so it lost those too.
func TestAnApostropheInsideAQuotedAttribute(t *testing.T) {
	page := []byte(`<html><head>
  <meta name="citation_title" content="What's Left of a Title">
  <meta name="citation_conference_title" content="CHI '24">
  <meta name='citation_author' content='O&#39;Hara, Kenton'>
</head></html>`)
	if got := titleFromBody(page); got != "What's Left of a Title" {
		t.Errorf("title = %q, truncated at the apostrophe", got)
	}
	if got := recordFromBody(page).Venue; got != "CHI '24" {
		t.Errorf("venue = %q, truncated at the apostrophe", got)
	}
	// A single-quoted attribute is read by its own delimiter too, and its
	// entity is decoded.
	if got := recordFromBody(page).Authors; len(got) != 1 || got[0] != "O'Hara, Kenton" {
		t.Errorf("authors = %v, want the single-quoted value whole", got)
	}
}

// TestTheAbstractComesFromTheSource checks that an abstract is the source's
// own, with its markup removed and nothing else changed. Crossref deposits
// JATS; arXiv publishes a <summary>; a publisher page may emit
// citation_abstract.
func TestTheAbstractComesFromTheSource(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"api.crossref.org": []byte(`{"message":{"title":["A Paper"],"abstract":"<jats:p>Abstract We argue that <jats:italic>context</jats:italic> is more than location.</jats:p>"}}`),
	}}
	id, err := Resolve(context.Background(), theDOI, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "We argue that context is more than location."
	if id.Record.Abstract != want {
		t.Errorf("abstract = %q, want %q", id.Record.Abstract, want)
	}

	feed := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <title>Retention Under Load</title>
    <summary>  We measure attention over a long session,
    and report the decline.  </summary>
  </entry>
</feed>`)
	arx := &fakeFetcher{responses: map[string][]byte{"export.arxiv.org": feed}}
	id, err = Resolve(context.Background(), "2603.01795", WithFetcher(arx))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Record.Abstract != "We measure attention over a long session, and report the decline." {
		t.Errorf("arXiv abstract = %q", id.Record.Abstract)
	}

	page := []byte(`<html><head>
  <meta name="citation_title" content="One Page">
  <meta name="citation_abstract" content="A short statement of what the paper does.">
</head></html>`)
	if got := recordFromBody(page).Abstract; got != "A short statement of what the paper does." {
		t.Errorf("page abstract = %q", got)
	}

	// A source with no abstract yields none, rather than the page's prose.
	bare := []byte(`<html><head><meta name="citation_title" content="No Abstract Here"></head><body>Some body text.</body></html>`)
	if got := recordFromBody(bare).Abstract; got != "" {
		t.Errorf("abstract = %q, want none — nothing published one", got)
	}
}

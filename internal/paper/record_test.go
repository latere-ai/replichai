package paper

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// A full Crossref work, of the shape the real service returns: authors with
// given and family parts, a container title, a date-parts issue date, and the
// volume/issue/page/publisher that used to be fetched and discarded.
const crossrefFull = `{"message":{
  "title":["Effects of Semantic Segmentation Visualization on Trust"],
  "author":[
    {"given":"Changkun","family":"Ou"},
    {"given":"Robin","family":"Welsch"},
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
		Authors:   []string{"Changkun Ou", "Robin Welsch", "The Working Group"},
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

// AC1, the other half: a record that declares less resolves anyway, with the
// fields nobody declared left empty rather than invented.
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

// A preprint that names a DOI becomes that DOI, and Crossref is then the better
// source for its record — but what Crossref omits, the preprint still supplies.
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
  <meta name="citation_author" content="Ou, Changkun">
  <meta name="citation_author" content="Welsch, Robin">
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
		// Kept as the page spells them: inverting a name is a guess about
		// somebody's actual name, and the owner can correct it.
		Authors:   []string{"Ou, Changkun", "Welsch, Robin"},
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

// AC3, the other half: a page that declares nothing about itself resolves to a
// paper with no record at all, rather than to a paper with an invented one.
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

// A conference page names its venue with citation_conference_title, and a page
// with a single page number cites one page rather than a range to itself.
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

// The DOI path falls back to the publisher's page for what Crossref does not
// answer — including a DOI Crossref has no record of at all.
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

// Enrichment is best-effort: a metadata service that is down or answering
// nonsense must not fail a resolution that already has its key.
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

// A record is what a citation is made of, and nothing in this package may write
// one from a model or from the paper's prose. The guard is the package's own
// source: every field is set from a parsed source, and this test fails if the
// package ever grows an import that could invent one.
func TestNothingInferred(t *testing.T) {
	rec := recordFromBody([]byte(`<html><body>Ada Lovelace wrote this in 1843 for the Journal of Everything.</body></html>`))
	if !rec.Empty() {
		t.Errorf("a record was read out of prose rather than a declaration: %+v", rec)
	}
	if strings.Contains(strings.Join(rec.Authors, ""), "Ada") {
		t.Error("an author was guessed from the body text")
	}
}

// The truncation this found: a conference is called CHI '24, and reading a
// double-quoted attribute up to the first apostrophe inside it lost the year.
// The same matcher reads the title and the DOI, so it lost those too.
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
	// A single-quoted attribute is read by its own delimiter too.
	if got := recordFromBody(page).Authors; len(got) != 1 || got[0] != "O&#39;Hara, Kenton" {
		t.Errorf("authors = %v, want the single-quoted value whole", got)
	}
}

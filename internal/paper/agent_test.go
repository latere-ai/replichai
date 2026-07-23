package paper

import (
	"context"
	"errors"
	"testing"
)

// pageWithoutMeta is a messy page: it names the DOI in running text but emits
// none of the citation_* meta tags, so the deterministic resolver misses it.
func pageWithoutMeta(doi string) []byte {
	return []byte(`<html><head><title>Some Paper</title></head><body>` +
		`<p>Accepted to CHI. DOI: ` + doi + `</p></body></html>`)
}

// An agent-backed resolver upgrades a page the meta standard can't read: it
// proposes the DOI, verification finds it in the page, and identity is doi.
func TestAgentResolverUpgradesMessyPage(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"authors.example": pageWithoutMeta(theDOI),
	}}
	agent := AgentPageResolver{Prompter: PrompterFunc(func(_ context.Context, _ string) (string, error) {
		return theDOI, nil
	})}
	id, err := Resolve(context.Background(), "https://authors.example/paper",
		WithFetcher(fetch), WithPageResolver(agent))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("got %s, want doi:%s", id, theDOI)
	}
}

// The load-bearing guarantee: a hallucinated identifier the page does not
// contain is rejected, so an LLM cannot invent or drift identity. The page has
// no real identifier, so resolution falls back to a content digest.
func TestAgentResolverCannotHallucinateIdentity(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"authors.example": []byte(`<html><body>a page with no identifier at all</body></html>`),
	}}
	agent := AgentPageResolver{Prompter: PrompterFunc(func(_ context.Context, _ string) (string, error) {
		return "10.9999/made-up-doi", nil // not present in the page
	})}
	id, err := Resolve(context.Background(), "https://authors.example/paper",
		WithFetcher(fetch), WithPageResolver(agent))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeSHA256 {
		t.Errorf("hallucinated DOI was accepted: got %s, want a content digest", id)
	}
}

// An agent proposing an arXiv ID re-enters the deterministic arXiv path, so it
// is looked up and upgraded to a DOI just like a direct arXiv reference.
func TestAgentResolverArXivCandidateReentersLookup(t *testing.T) {
	page := []byte(`<html><body>preprint at arXiv:2603.01795</body></html>`)
	fetch := &fakeFetcher{responses: map[string][]byte{
		"authors.example":  page,
		"export.arxiv.org": arxivFeedWithDOI(pleasQLarifyTitle, theDOI),
	}}
	agent := AgentPageResolver{Prompter: PrompterFunc(func(_ context.Context, _ string) (string, error) {
		return "2603.01795", nil
	})}
	id, err := Resolve(context.Background(), "https://authors.example/paper",
		WithFetcher(fetch), WithPageResolver(agent))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("got %s, want the arXiv candidate upgraded to doi:%s", id, theDOI)
	}
}

// A page carrying the citation_* meta standard is resolved from the tags
// directly; the model is never consulted.
func TestAgentResolverPrefersMetaOverModel(t *testing.T) {
	page := []byte(`<html><head><meta name="citation_doi" content="` + theDOI + `"></head></html>`)
	fetch := &fakeFetcher{responses: map[string][]byte{"pub.example": page}}
	agent := AgentPageResolver{Prompter: PrompterFunc(func(_ context.Context, _ string) (string, error) {
		t.Fatalf("model was consulted despite a citation_doi meta tag")
		return "", nil
	})}
	id, err := Resolve(context.Background(), "https://pub.example/paper",
		WithFetcher(fetch), WithPageResolver(agent))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("got %s, want doi:%s", id, theDOI)
	}
}

// A model failure is not licence to guess: resolution errors rather than
// minting a digest identity for a page that might carry a DOI.
func TestAgentResolverErrorPropagates(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"authors.example": []byte(`<html><body>no meta here</body></html>`),
	}}
	agent := AgentPageResolver{Prompter: PrompterFunc(func(_ context.Context, _ string) (string, error) {
		return "", errors.New("model unavailable")
	})}
	_, err := Resolve(context.Background(), "https://authors.example/paper",
		WithFetcher(fetch), WithPageResolver(agent))
	if !errors.Is(err, ErrUnresolvable) {
		t.Errorf("err = %v, want ErrUnresolvable", err)
	}
}

func TestParseCandidate(t *testing.T) {
	cases := map[string]string{
		"10.1145/x":     "10.1145/x",
		"  NONE  ":      "",
		"none":          "",
		"":              "",
		"2603.01795 v2": "2603.01795",
		"DOI: 10.1/abc": "DOI:", // first token; verification rejects it downstream
	}
	for in, want := range cases {
		if got := parseCandidate(in); got != want {
			t.Errorf("parseCandidate(%q) = %q, want %q", in, got, want)
		}
	}
}

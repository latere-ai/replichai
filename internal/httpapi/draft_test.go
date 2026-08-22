package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// fakeDrafter answers with a canned paragraph and records what it was asked, so
// a test can assert both the money and what left the building.
type fakeDrafter struct {
	model  string
	text   string
	usage  provider.Usage
	err    error
	prompt string
	system string
	calls  int
}

func (f *fakeDrafter) Name() string { return f.model }

func (f *fakeDrafter) Complete(_ context.Context, r draft.Request) (draft.Result, error) {
	f.calls++
	f.prompt, f.system = r.Prompt, r.System
	// The usage travels with a failure too, as the real models do when they
	// answer with nothing: those tokens were spent and are still billable.
	return draft.Result{Text: f.text, Model: f.model, Usage: f.usage}, f.err
}

// fixedPolicy is a platform settings source with one rate card.
type fixedPolicy struct{ settings platform.Settings }

func (p fixedPolicy) Settings(context.Context) (platform.Settings, error) { return p.settings, nil }

const draftModel = "small-1"

// pricedAt is a rate card that prices the drafting model and nothing else.
func pricedAt(input, output int64) platform.Settings {
	return platform.Settings{Rates: credit.Card{Models: []credit.Rates{{Model: draftModel, Input: input, Output: output}}}}
}

// draftDeps wires a project with credits, a priced drafting model, and a paper
// with a record — the ordinary case.
func draftDeps(t *testing.T, d *fakeDrafter, set platform.Settings) (Deps, string) {
	t.Helper()
	deps := testDeps(allowAll)
	// No Authority: these tests are about drafting, not about who may create
	// credits, which the ledger's own contract covers.
	ledger := credit.NewMemStore(nil, nil)
	deps.Credits = ledger
	deps.Reserve = func(context.Context) (int64, error) { return credit.Dollar, nil }
	if d != nil {
		deps.Drafter = d
	}
	deps.Platform = fixedPolicy{settings: set}

	ctx := context.Background()
	p, err := deps.Projects.Create(ctx, "me", "Trust in automation")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := deps.Projects.AddPaper(ctx, p.ID, project.Paper{
		Key: "doi:10.1145/1", Ref: "https://doi.org/10.1145/1", Title: "Effects of Segmentation Overlays", AddedBy: "me",
		Record: paper.Record{Authors: []string{"Ada Lovelace"}, Venue: "CHI '24", Year: 2024},
	}); err != nil {
		t.Fatalf("add paper: %v", err)
	}
	if err := ledger.Topup(ctx, access.Principal{Sub: "me", Email: "me@example.org"}, "me@example.org", 10*credit.Dollar); err != nil {
		t.Fatalf("topup: %v", err)
	}
	if err := ledger.Fund(ctx, "me@example.org", p.ID, "Trust in automation", 5*credit.Dollar); err != nil {
		t.Fatalf("fund: %v", err)
	}
	return deps, p.ID
}

// AC3 and AC4: a draft is written from what the project holds, charged to the
// project at the configured price, and stored nowhere.
func TestDraftingADescription(t *testing.T) {
	d := &fakeDrafter{
		model: draftModel,
		text:  "Two papers on whether a segmentation overlay changes what a driver trusts.",
		// A million input tokens at 1 and a million output at 5 would be 6 units;
		// these are the fractions that actually occur.
		usage: provider.Usage{InputTokens: 1_000_000, OutputTokens: 200_000},
	}
	deps, id := draftDeps(t, d, pricedAt(1_000_000, 5_000_000))
	h := NewRouter(deps).Handler()

	before, _ := deps.Credits.Balance(context.Background(), credit.Pot(id))
	w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("draft = %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Description string `json:"description"`
		Model       string `json:"model"`
		CostMicro   int64  `json:"cost_micro"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Description != d.text || got.Model != draftModel {
		t.Errorf("draft = %+v", got)
	}
	// 1M input at $1/Mtok + 0.2M output at $5/Mtok = $2.00.
	if got.CostMicro != 2*credit.Dollar {
		t.Errorf("cost = %d, want %d", got.CostMicro, 2*credit.Dollar)
	}
	after, _ := deps.Credits.Balance(context.Background(), credit.Pot(id))
	if before-after != 2*credit.Dollar {
		t.Errorf("the project was charged %d, want %d", before-after, 2*credit.Dollar)
	}

	// Written from the project, and stored nowhere: the description is still
	// empty until somebody saves what they read.
	if p, _ := deps.Projects.Get(context.Background(), id); p.Description != "" {
		t.Errorf("the draft was stored without anybody accepting it: %q", p.Description)
	}
	for _, want := range []string{"Trust in automation", "Effects of Segmentation Overlays", "Ada Lovelace"} {
		if !strings.Contains(d.prompt, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, d.prompt)
		}
	}
	if d.system != draft.DescriptionSystem {
		t.Error("the drafting rules are the platform's, and did not travel")
	}
}

// AC4, the other half: the charge is one ledger line of its own kind, so a
// project's statement accounts for what its drafts cost beside its sessions.
func TestADraftAppearsInTheStatement(t *testing.T) {
	d := &fakeDrafter{model: draftModel, text: "A paragraph.", usage: provider.Usage{InputTokens: 500_000}}
	deps, id := draftDeps(t, d, pricedAt(1_000_000, 5_000_000))
	h := NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusOK {
		t.Fatalf("draft = %d: %s", w.Code, w.Body.String())
	}
	entries, err := deps.Credits.Entries(context.Background(), credit.Pot(id), 20)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	var drafts int
	for _, e := range entries {
		if e.Kind == credit.KindDraft {
			drafts++
			if e.JobID != "" {
				t.Errorf("a draft charge names a session: %+v", e)
			}
		}
	}
	if drafts != 1 {
		t.Errorf("statement holds %d draft charges, want 1", drafts)
	}
}

// AC5, AC6, AC7 and AC9: every refusal, and which of them the owner is told
// about versus the operator.
func TestDraftingRefusals(t *testing.T) {
	good := func() *fakeDrafter {
		return &fakeDrafter{model: draftModel, text: "A paragraph.", usage: provider.Usage{InputTokens: 1000}}
	}

	// AC7: no model configured — the route says so, and 038 still works.
	deps, id := draftDeps(t, good(), pricedAt(1_000_000, 5_000_000))
	deps.Drafter = nil
	h := NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("with no model = %d, want 503", w.Code)
	}
	if w := do(t, h, "PATCH", "/projects/"+id, `{"description":"typed by hand"}`, "me"); w.Code != http.StatusOK {
		t.Errorf("a deployment that cannot draft cannot describe either: %d", w.Code)
	}

	// AC6: configured but unpriced — refused, and the model is never called.
	d := good()
	deps, id = draftDeps(t, d, platform.Settings{Rates: credit.Card{Models: []credit.Rates{{Model: "some-other-model", Input: 1}}}})
	h = NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("an unpriced model = %d, want 503", w.Code)
	}
	if d.calls != 0 {
		t.Error("an unpriced model was called anyway, and the tokens are unaccounted for")
	}

	// A card that prices it at nothing is not a price: free compute by typo.
	d = good()
	deps, id = draftDeps(t, d, pricedAt(0, 0))
	h = NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a model priced at zero = %d, want 503", w.Code)
	}
	if d.calls != 0 {
		t.Error("a model priced at nothing was called")
	}

	// AC5: a project with nothing available is refused before the call.
	d = good()
	deps = testDeps(allowAll)
	ledger := credit.NewMemStore(nil, nil)
	deps.Credits, deps.Drafter, deps.Platform = ledger, d, fixedPolicy{settings: pricedAt(1_000_000, 5_000_000)}
	broke, err := deps.Projects.Create(context.Background(), "me", "Unfunded")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	h = NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+broke.ID+"/description/draft", "", "me"); w.Code != http.StatusPaymentRequired {
		t.Errorf("an unfunded project = %d, want 402", w.Code)
	}
	if d.calls != 0 {
		t.Error("an unfunded project's draft was written and then not paid for")
	}

	// AC9: not a member, not your draft to ask for.
	deps, id = draftDeps(t, good(), pricedAt(1_000_000, 5_000_000))
	h = NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "stranger"); w.Code == http.StatusOK {
		t.Error("a stranger drafted for somebody else's project")
	}

	// A model that fails is a bad gateway, not a 500: it is somebody else's
	// endpoint, and the project was charged nothing.
	failing := &fakeDrafter{model: draftModel, err: errors.New("endpoint down")}
	deps, id = draftDeps(t, failing, pricedAt(1_000_000, 5_000_000))
	h = NewRouter(deps).Handler()
	before, _ := deps.Credits.Balance(context.Background(), credit.Pot(id))
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusBadGateway {
		t.Errorf("a failed call = %d, want 502", w.Code)
	}
	if after, _ := deps.Credits.Balance(context.Background(), credit.Pot(id)); after != before {
		t.Error("a call that produced nothing charged the project")
	}
}

// AC7, as the app sees it: the model list says whether this deployment drafts,
// so the screen can offer the control or not rather than offering one that
// answers 503.
func TestTheModelListSaysWhetherItDrafts(t *testing.T) {
	says := func(deps Deps) bool {
		w := do(t, NewRouter(deps).Handler(), "GET", "/models", "", "me")
		var got struct {
			Drafting bool `json:"drafting"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got.Drafting
	}
	configured, _ := draftDeps(t, &fakeDrafter{model: draftModel}, pricedAt(1_000_000, 5_000_000))
	if !says(configured) {
		t.Error("a deployment with a priced model says it cannot draft")
	}
	unpriced, _ := draftDeps(t, &fakeDrafter{model: draftModel}, platform.Settings{})
	if says(unpriced) {
		t.Error("a deployment whose model has no price offers a control that can only refuse")
	}
	none, _ := draftDeps(t, &fakeDrafter{model: draftModel}, pricedAt(1_000_000, 5_000_000))
	none.Drafter = nil
	if says(none) {
		t.Error("a deployment with no model says it drafts")
	}
}

// The balance check is read-then-spend and the spend lands after the model has
// answered, so a burst of requests would otherwise all read one healthy balance
// and all buy tokens. A session cannot do that — 011 holds the money before the
// work — and a draft has no job to hold against, so the bound is on the work.
func TestOnlyOneDraftAtATimePerProject(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	d := &blockingDrafter{model: draftModel, entered: entered, release: release}
	deps, id := draftDeps(t, nil, pricedAt(1_000_000, 5_000_000))
	deps.Drafter = d
	h := NewRouter(deps).Handler()

	done := make(chan int, 1)
	go func() {
		done <- do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me").Code
	}()
	<-entered // the first draft is inside the model call

	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusTooManyRequests {
		t.Errorf("a second concurrent draft = %d, want 429", w.Code)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("the first draft = %d", code)
	}
	if d.calls != 1 {
		t.Errorf("the model was called %d times for two requests", d.calls)
	}
	// And the slot is released, so the next one goes through.
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusOK {
		t.Errorf("the draft after the first finished = %d, want 200", w.Code)
	}
}

// blockingDrafter holds a call open until released, so a test can have two
// requests genuinely overlap.
type blockingDrafter struct {
	model   string
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (b *blockingDrafter) Name() string { return b.model }

func (b *blockingDrafter) Complete(_ context.Context, _ draft.Request) (draft.Result, error) {
	b.mu.Lock()
	b.calls++
	first := b.calls == 1
	b.mu.Unlock()
	if first {
		b.entered <- struct{}{}
		<-b.release
	}
	return draft.Result{Text: "A paragraph.", Model: b.model, Usage: provider.Usage{InputTokens: 1000}}, nil
}

// A model that answered with nothing is refused, and the project is still
// charged for the tokens the attempt burned. Refusing without charging would
// leave the deployment paying for the compute, and answering 200 would let the
// owner save a blank description over one they had.
func TestAnEmptyDraftIsChargedAndRefused(t *testing.T) {
	d := &fakeDrafter{
		model: draftModel,
		err:   draft.ErrEmptyDraft,
		usage: provider.Usage{InputTokens: 1_000_000, OutputTokens: 200_000},
	}
	deps, id := draftDeps(t, d, pricedAt(1_000_000, 5_000_000))
	h := NewRouter(deps).Handler()

	before, _ := deps.Credits.Balance(context.Background(), credit.Pot(id))
	w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("an empty draft = %d, want 502: %s", w.Code, w.Body.String())
	}
	var got struct {
		Error       string `json:"error"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The same code the blank answer has always been refused with, so the app
	// keeps telling the person the same thing.
	if got.Error != "drafting_empty" {
		t.Errorf("error = %q, want drafting_empty", got.Error)
	}
	// 1M input at $1/Mtok + 0.2M output at $5/Mtok = $2.00, spent either way.
	after, _ := deps.Credits.Balance(context.Background(), credit.Pot(id))
	if before-after != 2*credit.Dollar {
		t.Errorf("the project was charged %d for the spent tokens, want %d", before-after, 2*credit.Dollar)
	}
	entries, err := deps.Credits.Entries(context.Background(), credit.Pot(id), 20)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	var drafts int
	for _, e := range entries {
		if e.Kind == credit.KindDraft {
			drafts++
		}
	}
	if drafts != 1 {
		t.Errorf("statement holds %d draft charges, want 1", drafts)
	}
}

// The models in this repository refuse a blank with ErrEmptyDraft, but Drafter
// is an interface anybody can satisfy, so a blank answered as a success is
// refused here too rather than handed to the editor.
func TestABlankAnsweredAsASuccessIsStillRefused(t *testing.T) {
	d := &fakeDrafter{model: draftModel, text: "   ", usage: provider.Usage{InputTokens: 1_000_000}}
	deps, id := draftDeps(t, d, pricedAt(1_000_000, 5_000_000))
	h := NewRouter(deps).Handler()
	if w := do(t, h, "POST", "/projects/"+id+"/description/draft", "", "me"); w.Code != http.StatusBadGateway {
		t.Fatalf("a blank draft = %d, want 502: %s", w.Code, w.Body.String())
	}
	// Still charged, for the same reason the refusal above is.
	if after, _ := deps.Credits.Balance(context.Background(), credit.Pot(id)); after != 4*credit.Dollar {
		t.Errorf("balance = %d, want the dollar of tokens taken off 5", after)
	}
}

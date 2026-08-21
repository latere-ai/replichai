package job

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

func TestManagedSessionRefRoundTripsAndLegacyNeverUsesCurrentDefault(t *testing.T) {
	ref := managed.SessionRef{
		Runtime: "runtime-a", ID: "session-1", Revision: 3, LocatorVersion: 2,
		Locator: json.RawMessage(`{"future":{"container":"c-1"}}`),
	}
	raw, _ := json.Marshal(ref)
	job := Job{Status: provider.RunStatus{RunID: ref.ID, Runtime: string(ref.Runtime), SessionRef: raw}}
	got, ok, err := job.ManagedSessionRef()
	if err != nil || !ok || string(got.Locator) != string(ref.Locator) || got.Revision != 3 {
		t.Fatalf("ManagedSessionRef = %+v, %v, %v", got, ok, err)
	}
	legacy, ok, err := (Job{Status: provider.RunStatus{RunID: "legacy-id"}}).ManagedSessionRef()
	if err != nil || !ok || legacy.Runtime != LegacyRuntime || legacy.ID != "legacy-id" {
		t.Fatalf("legacy ManagedSessionRef = %+v, %v, %v", legacy, ok, err)
	}
	if _, ok, err := (Job{}).ManagedSessionRef(); err != nil || ok {
		t.Fatalf("empty ManagedSessionRef = %v, %v", ok, err)
	}
}

func TestJobCostUsesPersistedModelProvider(t *testing.T) {
	card := credit.Card{Models: []credit.Rates{
		{Model: "shared", Ref: managed.ModelRef{Provider: "anthropic", ID: "shared"}, Input: 3_000_000},
		{Model: "shared", Ref: managed.ModelRef{Provider: "openai", ID: "shared"}, Input: 9_000_000},
	}}
	j := Job{Rates: card, Status: provider.RunStatus{
		Model: "shared", ModelProvider: "openai", Usage: provider.Usage{InputTokens: 1},
	}}
	if got := j.cost(); got != 9 {
		t.Fatalf("OpenAI cost = %d, want 9", got)
	}
	// Historical rows had no provider and are Anthropic, never current-default.
	j.Status.ModelProvider = ""
	if got := j.cost(); got != 3 {
		t.Fatalf("legacy cost = %d, want 3", got)
	}
}

func TestManagedSessionRefRejectsMalformedAndMismatchedPersistence(t *testing.T) {
	for _, status := range []provider.RunStatus{
		{SessionRef: json.RawMessage(`not-json`)},
		{SessionRef: json.RawMessage(`{}`)},
		{Runtime: "runtime-b", SessionRef: json.RawMessage(`{"runtime":"runtime-a","id":"id","revision":1,"locator_version":1}`)},
	} {
		if _, _, err := (Job{Status: status}).ManagedSessionRef(); err == nil {
			t.Fatalf("invalid status accepted: %+v", status)
		} else if status.Runtime != "" && !errors.Is(err, managed.ErrRuntimeMismatch) {
			t.Fatalf("mismatch = %v", err)
		}
	}
}

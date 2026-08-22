package builtin

import (
	"context"
	"testing"

	"github.com/latere-ai/replichai/internal/managed/memory"
)

func TestCatalogComposesAdaptersExplicitly(t *testing.T) {
	store := memory.NewStore()
	catalog, err := Catalog(Config{MemoryStore: store, Anthropic: AnthropicConfig{APIKey: "test-key", BaseURL: "https://example.invalid"}})
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if got := catalog.Names(); len(got) != 2 || got[0] != "anthropic-managed" || got[1] != "memory" {
		t.Fatalf("Names = %v", got)
	}
	if runtime, err := catalog.Build(context.Background(), "memory"); err != nil || runtime.Name() != "memory" {
		t.Fatalf("Build(memory) = %v, %v", runtime, err)
	}
	if runtime, err := catalog.Build(context.Background(), string(AnthropicManaged)); err != nil || runtime.Name() != AnthropicManaged {
		t.Fatalf("Build(%s) = %v, %v", AnthropicManaged, runtime, err)
	}
	// The runtime answers to its own name only. "anthropic" named the vendor,
	// not the runtime, and an alias that conflates the two is how a second
	// vendor's runtime ends up unnameable.
	if _, err := catalog.Build(context.Background(), "anthropic"); err == nil {
		t.Fatal("the removed vendor alias still resolves")
	}
}

func TestAnthropicRequiresExplicitCredential(t *testing.T) {
	catalog, _ := Catalog(Config{})
	if _, err := catalog.Build(context.Background(), "anthropic-managed"); err == nil {
		t.Fatal("Anthropic runtime accepted no explicit credential")
	}
}

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
	if runtime, err := catalog.Build(context.Background(), "anthropic"); err != nil || runtime.Name() != AnthropicManaged {
		t.Fatalf("Build(anthropic) = %v, %v", runtime, err)
	}
}

func TestAnthropicRequiresExplicitCredential(t *testing.T) {
	catalog, _ := Catalog(Config{})
	if _, err := catalog.Build(context.Background(), "anthropic-managed"); err == nil {
		t.Fatal("Anthropic runtime accepted no explicit credential")
	}
}

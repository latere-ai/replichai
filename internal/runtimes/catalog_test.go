package runtimes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

func TestCatalogIsCanonicalSortedAndInstanceScoped(t *testing.T) {
	one, err := NewCatalog(
		Entry{Name: "z-runtime", Build: factory(&routerRuntime{name: "z-runtime"})},
		Entry{Name: "a-runtime", Aliases: []string{" LEGACY "}, Build: factory(&routerRuntime{name: "a-runtime"})},
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if got, want := one.Names(), []managed.RuntimeName{"a-runtime", "z-runtime"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	canonical, err := one.Canonical(" legacy ")
	if err != nil || canonical != "a-runtime" {
		t.Fatalf("Canonical = %q, %v", canonical, err)
	}
	two, err := NewCatalog(Entry{Name: "other", Build: factory(&routerRuntime{name: "other"})})
	if err != nil {
		t.Fatalf("second NewCatalog: %v", err)
	}
	if _, err := two.Canonical("legacy"); err == nil {
		t.Fatal("catalog instances leaked aliases")
	}
	if _, err := one.Canonical("missing"); err == nil || !strings.Contains(err.Error(), "a-runtime, z-runtime") {
		t.Fatalf("unknown runtime error = %v", err)
	}
}

func TestCatalogRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
	}{
		{"noncanonical", []Entry{{Name: " Upper ", Build: factory(&routerRuntime{name: "upper"})}}},
		{"nil factory", []Entry{{Name: "one"}}},
		{"duplicate name", []Entry{{Name: "one", Build: factory(&routerRuntime{name: "one"})}, {Name: "one", Build: factory(&routerRuntime{name: "one"})}}},
		{"empty alias", []Entry{{Name: "one", Aliases: []string{" "}, Build: factory(&routerRuntime{name: "one"})}}},
		{"alias name conflict", []Entry{{Name: "one", Aliases: []string{"two"}, Build: factory(&routerRuntime{name: "one"})}, {Name: "two", Build: factory(&routerRuntime{name: "two"})}}},
		{"duplicate alias", []Entry{{Name: "one", Aliases: []string{"legacy"}, Build: factory(&routerRuntime{name: "one"})}, {Name: "two", Aliases: []string{"legacy"}, Build: factory(&routerRuntime{name: "two"})}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCatalog(test.entries...); err == nil {
				t.Fatal("invalid catalog was accepted")
			}
		})
	}
}

func TestCatalogBuildAddsRuntimeContext(t *testing.T) {
	cause := errors.New("bad credentials")
	catalog, _ := NewCatalog(
		Entry{Name: "broken", Build: func(context.Context) (managed.Runtime, error) { return nil, cause }},
		Entry{Name: "nil", Build: func(context.Context) (managed.Runtime, error) { return nil, nil }},
		Entry{Name: "wrong", Build: factory(&routerRuntime{name: "other"})},
		Entry{Name: "good", Build: factory(&routerRuntime{name: "good"})},
	)
	if _, err := catalog.Build(context.Background(), "broken"); !errors.Is(err, cause) || !strings.Contains(err.Error(), `"broken"`) {
		t.Fatalf("broken Build = %v", err)
	}
	if _, err := catalog.Build(context.Background(), "nil"); err == nil {
		t.Fatal("nil runtime accepted")
	}
	if _, err := catalog.Build(context.Background(), "wrong"); err == nil {
		t.Fatal("wrong runtime identity accepted")
	}
	if runtime, err := catalog.Build(context.Background(), "good"); err != nil || runtime.Name() != "good" {
		t.Fatalf("good Build = %v, %v", runtime, err)
	}
}

func factory(runtime managed.Runtime) Factory {
	return func(context.Context) (managed.Runtime, error) { return runtime, nil }
}

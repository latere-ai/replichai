// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes

import (
	"context"
	"errors"
	"testing"

	managed "latere.ai/x/managed-agents"
)

type modelRuntime struct {
	*routerRuntime
	models []managed.Model
	err    error
}

func (runtime modelRuntime) Models(context.Context) ([]managed.Model, error) {
	return runtime.models, runtime.err
}

func TestModelResolverIsDeterministic(t *testing.T) {
	runtime := modelRuntime{routerRuntime: &routerRuntime{name: "runtime"}, models: []managed.Model{
		{Ref: managed.ModelRef{Provider: "provider", ID: "best-model"}, Preferences: []string{"best"}},
		{Ref: managed.ModelRef{Provider: "provider", ID: "fast-model"}, Preferences: []string{"fast"}},
	}}
	resolver := ModelResolver{Runtime: runtime}
	if model, err := resolver.ResolveModel(context.Background(), "best"); err != nil || model != "best-model" {
		t.Fatalf("ResolveModel = %q, %v", model, err)
	}
	if _, err := resolver.ResolveModel(context.Background(), "balanced"); err == nil {
		t.Fatal("missing preference used a catalog fallback")
	}
	runtime.models = append(runtime.models, managed.Model{Ref: managed.ModelRef{Provider: "provider", ID: "another"}, Preferences: []string{"best"}})
	if _, err := (ModelResolver{Runtime: runtime}).ResolveModel(context.Background(), "best"); err == nil {
		t.Fatal("ambiguous preference accepted")
	}
	injected := errors.New("catalog unavailable")
	if _, err := (ModelResolver{Runtime: modelRuntime{routerRuntime: runtime.routerRuntime, err: injected}}).ResolveModel(context.Background(), "best"); !errors.Is(err, injected) {
		t.Fatalf("runtime error = %v", err)
	}
}

// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

type routerRuntime struct {
	name  managed.RuntimeName
	calls []string
}

func (r *routerRuntime) Name() managed.RuntimeName { return r.name }
func (r *routerRuntime) Capabilities(context.Context) (managed.Capabilities, error) {
	r.calls = append(r.calls, "capabilities")
	return managed.Capabilities{}, nil
}
func (r *routerRuntime) Models(context.Context) ([]managed.Model, error) {
	r.calls = append(r.calls, "models")
	return nil, nil
}
func (r *routerRuntime) Validate(managed.SessionSpec) error {
	r.calls = append(r.calls, "validate")
	return nil
}
func (r *routerRuntime) Start(context.Context, managed.SessionSpec) (managed.SessionRef, error) {
	r.calls = append(r.calls, "start")
	return runtimeRef(r.name), nil
}
func (r *routerRuntime) Poll(context.Context, managed.SessionRef) (managed.Status, error) {
	r.calls = append(r.calls, "poll")
	return managed.Status{}, nil
}
func (r *routerRuntime) Events(context.Context, managed.SessionRef) ([]managed.Event, error) {
	r.calls = append(r.calls, "events")
	return nil, nil
}
func (r *routerRuntime) ListArtifacts(context.Context, managed.SessionRef) ([]managed.Artifact, error) {
	r.calls = append(r.calls, "list")
	return []managed.Artifact{{ID: "one", Path: "one.txt"}}, nil
}
func (r *routerRuntime) FetchArtifact(context.Context, managed.SessionRef, managed.Artifact) ([]byte, error) {
	r.calls = append(r.calls, "fetch")
	return nil, nil
}
func (r *routerRuntime) Cancel(context.Context, managed.SessionRef) error {
	r.calls = append(r.calls, "cancel")
	return nil
}
func (r *routerRuntime) Release(context.Context, managed.SessionRef) error {
	r.calls = append(r.calls, "release")
	return nil
}

func TestRouterPinsExistingReferencesWhenDefaultChanges(t *testing.T) {
	a := &routerRuntime{name: "runtime-a"}
	b := &routerRuntime{name: "runtime-b"}
	router, err := NewRouter("runtime-a", a, b)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	selected, _ := router.ForNew(managed.SessionSpec{})
	ref, _ := selected.Start(context.Background(), managed.SessionSpec{})
	flipped, err := router.WithDefault("runtime-b")
	if err != nil {
		t.Fatalf("WithDefault: %v", err)
	}
	if selected, _ := flipped.ForNew(managed.SessionSpec{}); selected != b {
		t.Fatal("new work did not use the new default")
	}
	runtime, err := flipped.ForRef(ref)
	if err != nil || runtime != a {
		t.Fatalf("ForRef = %v, %v", runtime, err)
	}
	ctx := context.Background()
	status, _ := runtime.Poll(ctx, ref)
	_ = status
	_, _ = runtime.Events(ctx, ref)
	artifacts, _ := runtime.ListArtifacts(ctx, ref)
	_, _ = runtime.FetchArtifact(ctx, ref, artifacts[0])
	_ = runtime.Cancel(ctx, ref)
	_ = runtime.Release(ctx, ref)
	wantA := []string{"start", "poll", "events", "list", "fetch", "cancel", "release"}
	if !reflect.DeepEqual(a.calls, wantA) || len(b.calls) != 0 {
		t.Fatalf("calls A=%v B=%v", a.calls, b.calls)
	}
}

func TestBoundRouteOutlivesDefaultAndRoundTrips(t *testing.T) {
	a := &routerRuntime{name: "runtime-a"}
	b := &routerRuntime{name: "runtime-b"}
	router, _ := NewRouter("runtime-a", a, b)
	route := router.Route()
	flipped, _ := router.WithDefault("runtime-b")
	bound, err := flipped.Bind(route)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	selected, err := bound.ForNew(managed.SessionSpec{})
	if err != nil || selected != a {
		t.Fatalf("bound ForNew = %v, %v", selected, err)
	}
	if runtime, err := bound.ForRef(runtimeRef("runtime-b")); err != nil || runtime != b {
		t.Fatalf("bound ForRef = %v, %v", runtime, err)
	}
}

func TestRouterRejectsInvalidConstructionRoutesAndReferences(t *testing.T) {
	a := &routerRuntime{name: "runtime-a"}
	for _, test := range []struct {
		name string
		call func() error
	}{
		{"nil runtime", func() error { _, err := NewRouter("runtime-a", nil); return err }},
		{"invalid name", func() error { _, err := NewRouter("Bad", &routerRuntime{name: "Bad"}); return err }},
		{"duplicate", func() error { _, err := NewRouter("runtime-a", a, &routerRuntime{name: "runtime-a"}); return err }},
		{"missing default", func() error { _, err := NewRouter("missing", a); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("invalid construction accepted")
			}
		})
	}
	router, _ := NewRouter("runtime-a", a)
	if _, err := router.WithDefault("missing"); err == nil {
		t.Fatal("missing default accepted")
	}
	for _, route := range []Route{{Version: 2, Runtime: "runtime-a"}, {Version: 1, Runtime: " Bad "}, {Version: 1, Runtime: "missing"}} {
		if _, err := router.Bind(route); err == nil {
			t.Fatalf("Bind(%+v) succeeded", route)
		}
	}
	badRef := runtimeRef("runtime-a")
	badRef.ID = ""
	if _, err := router.ForRef(badRef); !errors.Is(err, managed.ErrInvalidSpec) {
		t.Fatalf("invalid ref = %v", err)
	}
	if _, err := router.ForRef(runtimeRef("missing")); err == nil || !strings.Contains(err.Error(), "runtime-a") {
		t.Fatalf("missing ref runtime = %v", err)
	}
}

func TestRouterDetectsInstalledRuntimeIdentityDrift(t *testing.T) {
	runtime := &routerRuntime{name: "runtime-a"}
	router, _ := NewRouter("runtime-a", runtime)
	runtime.name = "runtime-b"
	if _, err := router.ForNew(managed.SessionSpec{}); !errors.Is(err, managed.ErrRuntimeMismatch) {
		t.Fatalf("identity drift = %v", err)
	}
}

func runtimeRef(runtime managed.RuntimeName) managed.SessionRef {
	return managed.SessionRef{Runtime: runtime, ID: "session", Revision: 1, LocatorVersion: 1}
}

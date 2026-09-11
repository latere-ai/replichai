// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/publish"
)

// fakeForge is an in-memory reproduction host: a movable head and a file tree
// per commit. It is the whole of what resolution needs, with no network.
type fakeForge struct {
	head  string
	files map[string]map[string][]byte // commit -> path -> content
}

func (f *fakeForge) Head(context.Context, publish.Repo) (string, error) { return f.head, nil }

func (f *fakeForge) ReadFileAt(_ context.Context, _ publish.Repo, commit, path string) ([]byte, bool, error) {
	tree, ok := f.files[commit]
	if !ok {
		return nil, false, nil
	}
	b, ok := tree[path]
	return b, ok, nil
}

func instrumentJSON(t *testing.T, design string) []byte {
	t.Helper()
	raw, err := json.Marshal(instrument.Instrument{Protocol: &instrument.Protocol{Design: design}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

var testRepo = publish.Repo{Owner: "open-hai", Name: "a-repro"}

// AC1: an instrument resolves at a pinned commit, and re-resolving the same ref
// after the reproduction gains a commit returns the pinned declaration, so the
// store need keep no copy; the ref is enough.
func TestResolvePinsToACommit(t *testing.T) {
	f := &fakeForge{
		head: "aaaaaaaa1111",
		files: map[string]map[string][]byte{
			"aaaaaaaa1111": {InstrumentPath: instrumentJSON(t, "design-v1")},
		},
	}

	first, err := Resolve(context.Background(), f, testRepo)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first.Ref.Commit != "aaaaaaaa1111" {
		t.Errorf("pinned commit = %q, want the head", first.Ref.Commit)
	}
	if first.Decl.Protocol.Design != "design-v1" {
		t.Errorf("resolved design = %q, want design-v1", first.Decl.Protocol.Design)
	}

	// The reproduction gains a commit with a changed instrument, and head moves.
	f.files["bbbbbbbb2222"] = map[string][]byte{InstrumentPath: instrumentJSON(t, "design-v2")}
	f.head = "bbbbbbbb2222"

	// Re-resolving the original ref still yields the pinned declaration.
	pinned, err := ResolveAt(context.Background(), f, first.Ref)
	if err != nil {
		t.Fatalf("ResolveAt: %v", err)
	}
	if pinned.Decl.Protocol.Design != "design-v1" {
		t.Errorf("pinned re-resolution = %q, want design-v1 (head moved to v2)", pinned.Decl.Protocol.Design)
	}

	// Resolving afresh follows the moved head, which shows that the pin, not
	// the forge, held the first result steady.
	latest, err := Resolve(context.Background(), f, testRepo)
	if err != nil {
		t.Fatalf("Resolve latest: %v", err)
	}
	if latest.Ref.Commit != "bbbbbbbb2222" || latest.Decl.Protocol.Design != "design-v2" {
		t.Errorf("fresh resolve = %q/%q, want the new head/design-v2", latest.Ref.Commit, latest.Decl.Protocol.Design)
	}
}

// AC1, second clause: the store keeps no copy of the instrument declaration,
// only a reference. This is asserted structurally, so swapping Experiment.Ref
// for a stored Instrument fails here rather than silently persisting src/ and
// instrument.json against 003's no-drift rule.
func TestStoredExperimentHoldsNoDeclaration(t *testing.T) {
	declType := reflect.TypeFor[Instrument]()
	stored := reflect.TypeFor[Experiment]()
	for field := range stored.Fields() {
		if field.Type == declType {
			t.Errorf("Experiment.%s stores a resolved Instrument — persist a reference, not a copy",
				field.Name)
		}
	}
	// The reference carries only repo + commit, no declaration.
	refType := reflect.TypeFor[InstrumentRef]()
	for field := range refType.Fields() {
		if field.Type == declType || field.Type == reflect.TypeFor[instrument.Instrument]() {
			t.Errorf("InstrumentRef.%s embeds a declaration — it must be a bare reference", field.Name)
		}
	}
}

func TestResolveErrors(t *testing.T) {
	t.Run("no instrument.json", func(t *testing.T) {
		f := &fakeForge{head: "c", files: map[string]map[string][]byte{"c": {}}}
		if _, err := Resolve(context.Background(), f, testRepo); !errors.Is(err, ErrNoInstrument) {
			t.Fatalf("want ErrNoInstrument, got %v", err)
		}
	})

	t.Run("malformed instrument.json", func(t *testing.T) {
		f := &fakeForge{head: "c", files: map[string]map[string][]byte{"c": {InstrumentPath: []byte("not json")}}}
		if _, err := Resolve(context.Background(), f, testRepo); err == nil {
			t.Fatal("a malformed instrument should error")
		}
	})
}

// errForge fails whichever call is set, so resolution's error wrapping on both
// the pin and the read is exercised.
type errForge struct {
	headErr, readErr error
	head             string
}

func (e errForge) Head(context.Context, publish.Repo) (string, error) { return e.head, e.headErr }
func (e errForge) ReadFileAt(context.Context, publish.Repo, string, string) ([]byte, bool, error) {
	return nil, false, e.readErr
}

func TestResolveWrapsForgeErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Resolve(context.Background(), errForge{headErr: boom}, testRepo); err == nil {
		t.Error("a head failure should propagate")
	}
	if _, err := Resolve(context.Background(), errForge{head: "abc", readErr: boom}, testRepo); err == nil {
		t.Error("a read failure should propagate")
	}
}

func TestSourceOffline(t *testing.T) {
	for _, s := range []Source{SourceReleased, SourceUploaded} {
		if !s.Offline() {
			t.Errorf("%s should be offline", s)
		}
	}
	for _, s := range []Source{SourceSynthetic, SourceHuman} {
		if s.Offline() {
			t.Errorf("%s should not be offline", s)
		}
	}
}

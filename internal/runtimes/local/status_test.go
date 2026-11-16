// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"encoding/json/v2"
	"os"
	"testing"
	"time"

	"latere.ai/x/pkg/statemachine/statemachinetest"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
)

func newStatusRuntime(t *testing.T) *Runtime {
	t.Helper()
	registry, err := harness.NewRegistry(harness.NewClaudeCode())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	catalog, err := NewCatalog(DefaultCatalogFile(), registry, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	runtime, err := New(Config{
		Sandbox: localtest.New(localtest.Script{}), Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runtime
}

// refTo names the session in dir the way a started session's reference does.
func refTo(t *testing.T, dir, id string) managed.SessionRef {
	t.Helper()
	locator, err := json.Marshal(Locator{Dir: dir})
	if err != nil {
		t.Fatalf("encode locator: %v", err)
	}
	return managed.SessionRef{Runtime: Name, ID: id, Locator: locator}
}

// The session's status writer, taken under the session's lock as Poll and
// Cancel take it, follows managed.Sessions alone and when two writers race.
func TestTheStatusWriterFollowsTheSessionTable(t *testing.T) {
	runtime := newStatusRuntime(t)
	var count int
	sessions := map[string]*Session{}
	driver := statemachinetest.Driver[managed.State]{
		Enter: func(t *testing.T, state managed.State) string {
			count++
			session, err := CreateSession(runtime.config.Root, "s"+time.Now().Format("150405.000000")+string(rune('a'+count%26))+string(rune('a'+count/26%26)), os.LookupEnv)
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			if err := session.WriteStatus(managed.Status{State: state}); err != nil {
				t.Fatalf("WriteStatus: %v", err)
			}
			sessions[session.ID()] = session
			return session.ID()
		},
		Move: func(key string, to managed.State) error {
			session := sessions[key]
			unlock, err := runtime.lock(refTo(t, session.Dir(), key))
			if err != nil {
				return err
			}
			defer unlock()
			return session.MoveStatus(managed.Status{State: to})
		},
		State: func(t *testing.T, key string) managed.State {
			status, err := sessions[key].ReadStatus()
			if err != nil {
				t.Fatalf("ReadStatus: %v", err)
			}
			return status.State
		},
	}
	statemachinetest.Exhaustive(t, managed.Sessions, driver)
	statemachinetest.Race(t, managed.Sessions, driver)
}

// A second runtime, standing in for another process, waits for the lock the
// first holds on a session.
func TestTheSessionLockHoldsAcrossRuntimes(t *testing.T) {
	first, second := newStatusRuntime(t), newStatusRuntime(t)
	session, err := CreateSession(first.config.Root, "locked", os.LookupEnv)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ref := refTo(t, session.Dir(), "locked")
	unlock, err := first.lock(ref)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	acquired := make(chan struct{})
	go func() {
		release, err := second.lock(ref)
		if err != nil {
			t.Errorf("second lock: %v", err)
			close(acquired)
			return
		}
		close(acquired)
		release()
	}()
	select {
	case <-acquired:
		t.Fatal("a second runtime took the lock while the first held it")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the second runtime never took the lock after the first released it")
	}
}

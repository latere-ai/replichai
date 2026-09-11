// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestSaveSurvivesConcurrentWriters checks that concurrent writers of one
// state path each leave a complete file. A fixed temporary name would let one
// writer rename the other's half-written bytes into place.
func TestSaveSurvivesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			s := State{StartedAt: time.Unix(int64(i), 0).UTC()}
			for j := range 200 {
				s.Entries = append(s.Entries, EntryState{Paper: fmt.Sprintf("10.1/%d-%d", i, j), Phase: Running})
			}
			errs <- s.Save(path)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState after concurrent saves: %v", err)
	}
	if len(got.Entries) != 200 {
		t.Fatalf("state has %d entries, want a complete file with 200", len(got.Entries))
	}
}

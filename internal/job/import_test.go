// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// refusingReserve fails the test if it is called, so a path that must never
// reach the ledger is shown not to, rather than assumed.
func refusingReserve(t *testing.T) credit.Reserve {
	return func(context.Context) (int64, error) {
		t.Fatal("an imported session reached the credit reserve")
		return 0, nil
	}
}

func imported(project string) Import {
	return Import{
		Principal: "user-1", Project: project, Ref: "10.1145/3654777.3676417",
		PaperKey: "doi:10.1145/3654777.3676417", Runtime: "local", ExternalID: "7f3a91c2",
		Phase: PhaseDone, RepoName: "open-hai/a-paper-xyz",
		Status:     provider.RunStatus{Runtime: "local", Usage: provider.Usage{InputTokens: 1000, OutputTokens: 200}},
		StartedAt:  time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 8, 25, 10, 42, 0, 0, time.UTC),
	}
}

// TestImportNeverReachesTheLedger checks that the import has no path to the
// reserve. The credit reserve here fails the test if it is called, so the test
// covers any branch added later as well.
func TestImportNeverReachesTheLedger(t *testing.T) {
	store := NewMemStore(WithCredits(nil, refusingReserve(t)))
	job, err := store.ImportLocal(context.Background(), imported("p-1"))
	if err != nil {
		t.Fatalf("ImportLocal: %v", err)
	}
	if job.Origin != OriginLocal || job.ExternalID != "7f3a91c2" {
		t.Fatalf("job = %+v", job)
	}
	if job.Phase != PhaseDone || job.RepoName != "open-hai/a-paper-xyz" {
		t.Fatalf("job = %+v", job)
	}
	// The times are when the work happened on the operator's machine, not
	// when the record arrived here.
	if !job.CreatedAt.Equal(imported("p-1").StartedAt) {
		t.Errorf("created = %s", job.CreatedAt)
	}
	if at := job.PhaseAt[PhaseDone]; !at.Equal(imported("p-1").FinishedAt) {
		t.Errorf("finished = %s", at)
	}
}

// TestOnlyATerminalSessionMayBeImported checks that only a finished session
// may be imported. A run still in progress is not a record, and a non-terminal
// row would be a job that no worker owns and no lease heartbeats.
func TestOnlyATerminalSessionMayBeImported(t *testing.T) {
	store := NewMemStore()
	for _, phase := range []Phase{PhaseSubmitted, PhaseResolving, PhaseRunning, PhasePublishing} {
		in := imported("p-1")
		in.Phase = phase
		_, err := store.ImportLocal(context.Background(), in)
		if !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("importing a %s session = %v", phase, err)
		}
	}
	for _, phase := range []Phase{PhaseDone, PhaseFailed} {
		in := imported("p-1")
		in.Phase, in.ExternalID = phase, string(phase)
		if _, err := store.ImportLocal(context.Background(), in); err != nil {
			t.Errorf("importing a %s session = %v", phase, err)
		}
	}
	// A session with no runtime or no id cannot be made idempotent, so it is
	// refused rather than stored as a duplicate that no re-push can update.
	for _, in := range []Import{
		func() Import { i := imported("p-1"); i.Runtime = ""; return i }(),
		func() Import { i := imported("p-1"); i.ExternalID = ""; return i }(),
		func() Import { i := imported("p-1"); i.Ref = ""; return i }(),
	} {
		if _, err := store.ImportLocal(context.Background(), in); err == nil {
			t.Errorf("an unidentifiable import was accepted: %+v", in)
		}
	}
}

// TestReimportingUpdatesOneRow checks that re-pushing the same session updates
// its row, so an interrupted upload is resumed by running it again.
func TestReimportingUpdatesOneRow(t *testing.T) {
	store := NewMemStore()
	first, err := store.ImportLocal(context.Background(), imported("p-1"))
	if err != nil {
		t.Fatalf("ImportLocal: %v", err)
	}
	changed := imported("p-1")
	changed.RepoName = "open-hai/a-paper-xyz-2"
	changed.Status.Usage.OutputTokens = 999
	second, err := store.ImportLocal(context.Background(), changed)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("a re-push opened a second session: %s then %s", first.ID, second.ID)
	}
	if second.RepoName != "open-hai/a-paper-xyz-2" || second.Status.Usage.OutputTokens != 999 {
		t.Fatalf("the re-push did not update the row: %+v", second)
	}
	// A different session is a different row.
	other := imported("p-1")
	other.ExternalID = "a91c27f3"
	third, err := store.ImportLocal(context.Background(), other)
	if err != nil || third.ID == first.ID {
		t.Fatalf("a second session shared a row: %v", err)
	}
	all, err := store.ListByProject(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("%d rows for two sessions", len(all))
	}
}

// TestSpendExcludesALocalSession checks that a local session is left out of
// the spend figure: the rail shows what a project cost, and this deployment
// charged nothing for a local session.
func TestSpendExcludesALocalSession(t *testing.T) {
	store := NewMemStore()
	if _, err := store.ImportLocal(context.Background(), imported("p-1")); err != nil {
		t.Fatalf("ImportLocal: %v", err)
	}
	spend, err := store.SpendSince(context.Background(), []string{"p-1"}, time.Time{})
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if spend["p-1"] != 0 {
		t.Fatalf("a locally-executed session was charged to the project: %d", spend["p-1"])
	}
}

// TestAnUnknownOriginIsRefused checks that an unknown origin is rejected when
// decoded rather than arriving as an empty string that is read as platform.
func TestAnUnknownOriginIsRefused(t *testing.T) {
	var origin Origin
	if err := origin.UnmarshalJSON([]byte(`"local"`)); err != nil || origin != OriginLocal {
		t.Fatalf("local = %q, %v", origin, err)
	}
	if err := origin.UnmarshalJSON([]byte(`"somewhere-else"`)); err == nil {
		t.Fatal("an unknown origin was accepted")
	}
	// Every row written before the column existed carries the empty value,
	// which means platform.
	var empty Origin
	if !empty.OrDefault().Metered() || empty.OrDefault() != OriginPlatform {
		t.Fatalf("the empty origin = %q", empty.OrDefault())
	}
	if OriginLocal.Metered() {
		t.Fatal("a local session was reported as metered")
	}
}

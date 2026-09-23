// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// runStoreContract asserts the invariants every Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is assumed
// empty.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// Enqueue and read back.
	j, err := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "arxiv:2302.03714", Kind: KindAudit, Project: "proj"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseSubmitted || got.Ref != "arxiv:2302.03714" || got.ProjectID != "proj" {
		t.Fatalf("read-back = %+v", got)
	}

	// An illegal transition is refused and does not move the job.
	if err := s.Advance(ctx, j.ID, PhaseRunning, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("illegal transition: want ErrIllegalTransition, got %v", err)
	}

	// The full lifecycle walks through the transition function, recording each.
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, p, func(jb *Job) {
			if p == PhasePublishing {
				jb.RepoName = "open-hai/x"
			}
		}); err != nil {
			t.Fatalf("advance to %s: %v", p, err)
		}
	}
	done, _ := s.Get(ctx, j.ID)
	if done.Phase != PhaseDone || done.RepoName != "open-hai/x" {
		t.Fatalf("final = %+v", done)
	}
	for _, p := range []Phase{PhaseSubmitted, PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if done.PhaseAt[p].IsZero() {
			t.Errorf("phase %s has no timestamp", p)
		}
	}

	// Observe records status in place, without a phase change.
	obs, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	if err := s.Observe(ctx, obs.ID, provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: 7}}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got, _ := s.Get(ctx, obs.ID); got.Status.Usage.InputTokens != 7 || got.Phase != PhaseSubmitted {
		t.Errorf("observe: status=%+v phase=%s", got.Status, got.Phase)
	}

	// A job in flight belongs to one driver. Two replicas reconcile the same rows
	// at startup, so the lease stops both of them rejoining one backend session;
	// without it the losing driver's failure overwrites the winner's published
	// success.
	lj, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "leased", Kind: KindAudit})
	if won, err := s.Adopt(ctx, lj.ID, "pod-a", time.Minute); err != nil || !won {
		t.Fatalf("first Adopt = %v/%v, want it won", won, err)
	}
	if won, err := s.Adopt(ctx, lj.ID, "pod-b", time.Minute); err != nil || won {
		t.Fatalf("second Adopt = %v/%v, want it refused — pod-a holds the lease", won, err)
	}
	// A driver that comes back under its own name resumes its own work, rather
	// than being locked out of it by the lease it took before it died.
	if won, err := s.Adopt(ctx, lj.ID, "pod-a", time.Minute); err != nil || !won {
		t.Fatalf("re-Adopt by the holder = %v/%v, want it won", won, err)
	}
	// A lease older than the ttl means the driver stopped reporting that it was
	// alive, and its run is adoptable; otherwise a crash would strand it.
	if won, err := s.Adopt(ctx, lj.ID, "pod-b", 0); err != nil || !won {
		t.Fatalf("Adopt of an expired lease = %v/%v, want it won", won, err)
	}
	// Claim leases the row as it claims it, so a session one pod just claimed
	// does not appear stranded to a pod starting up beside it.
	claimed, ok, err := s.Claim(ctx, "pod-a")
	if err != nil || !ok {
		t.Fatalf("Claim = %v/%v, want a job", ok, err)
	}
	if won, err := s.Adopt(ctx, claimed.ID, "pod-b", time.Minute); err != nil || won {
		t.Fatalf("Adopt of a just-claimed job = %v/%v, want it refused", won, err)
	}
	// Claim records the driver's name, not just the time. Almost every job in
	// production arrives through Claim rather than Adopt, and the worker's
	// heartbeat checks whether it still holds the lease by name, so a claim that
	// recorded a timestamp and no holder would make every ordinary run report
	// its lease lost and yield within a third of the ttl (044c). Adopt's own
	// predicate cannot detect the difference, which is why it is asserted here.
	if held, err := s.Touch(ctx, claimed.ID, "pod-a"); err != nil || !held {
		t.Fatalf("Touch of a job this worker claimed = %v/%v, want it held", held, err)
	}
	// A driver reports that it is alive without reporting a change. Observe
	// fires only on a changed status, so a quiet run would otherwise let its
	// lease expire while working correctly, and another replica would adopt a
	// live session (044c).
	tj, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "touched", Kind: KindAudit})
	if _, err := s.Adopt(ctx, tj.ID, "pod-a", time.Minute); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if held, err := s.Touch(ctx, tj.ID, "pod-a"); err != nil || !held {
		t.Fatalf("Touch by the holder = %v/%v, want it held", held, err)
	}
	// Touch never takes a lease: a worker that is not the holder is told so,
	// and the holder's lease is unchanged.
	if held, err := s.Touch(ctx, tj.ID, "pod-b"); err != nil || held {
		t.Fatalf("Touch by a non-holder = %v/%v, want it refused", held, err)
	}
	if won, err := s.Adopt(ctx, tj.ID, "pod-b", time.Minute); err != nil || won {
		t.Fatalf("Adopt after a foreign Touch = %v/%v, want pod-a to still hold it", won, err)
	}
	// A row that went terminal under its driver reports the same result as a
	// lost lease: stop driving.
	_ = s.Advance(ctx, tj.ID, PhaseFailed, func(jb *Job) { jb.Err = "done with" })
	if held, err := s.Touch(ctx, tj.ID, "pod-a"); err != nil || held {
		t.Fatalf("Touch of a terminal job = %v/%v, want it refused", held, err)
	}

	// A finished session cannot be adopted.
	_ = s.Advance(ctx, lj.ID, PhaseFailed, func(jb *Job) { jb.Err = "done with" })
	if won, err := s.Adopt(ctx, lj.ID, "pod-c", time.Minute); err != nil || won {
		t.Fatalf("Adopt of a terminal job = %v/%v, want it refused", won, err)
	}

	// A failure records its cause and lands in failed (submitted -> failed).
	fj, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_ = s.Advance(ctx, fj.ID, PhaseFailed, func(jb *Job) { jb.Err = "boom" })
	if got, _ := s.Get(ctx, fj.ID); got.Phase != PhaseFailed || got.Err != "boom" {
		t.Errorf("failure: phase=%s err=%q", got.Phase, got.Err)
	}

	// Active excludes terminal jobs.
	active, err := s.Active(ctx)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	for _, a := range active {
		if a.Phase.Terminal() {
			t.Errorf("Active returned a terminal job: %s", a.Phase)
		}
	}

	// ListByPrincipal is scoped to one owner and newest-first. Enqueue two jobs
	// for "owner" and one for a stranger, then assert the stranger's job never
	// appears and the two are ordered newest-first.
	first, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-a", Kind: KindAudit})
	second, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-b", Kind: KindAudit})
	_, _ = s.Enqueue(ctx, Submission{Principal: "stranger", Ref: "ref-c", Kind: KindAudit})
	mine, err := s.ListByPrincipal(ctx, "owner")
	if err != nil {
		t.Fatalf("ListByPrincipal: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("owner has %d jobs, want 2 (no stranger leak)", len(mine))
	}
	if mine[0].ID != second.ID || mine[1].ID != first.ID {
		t.Errorf("ListByPrincipal not newest-first: %s,%s want %s,%s", mine[0].ID, mine[1].ID, second.ID, first.ID)
	}
	for _, j := range mine {
		if j.Principal != "owner" {
			t.Errorf("ListByPrincipal leaked a %q job", j.Principal)
		}
	}
	// An unknown principal lists nothing, without error.
	if none, err := s.ListByPrincipal(ctx, "nobody"); err != nil || len(none) != 0 {
		t.Errorf("unknown principal: %d jobs, err %v; want 0, nil", len(none), err)
	}

	// The chosen model tier is durable (031). The worker that runs the session
	// reads this row and nothing else, because the submission is gone once
	// Enqueue returns, so a tier that did not survive the write would reach no
	// run, and the session would silently take the default its owner declined.
	chosen, err := s.Enqueue(ctx, Submission{
		Principal: "sub", Ref: "tiered", Kind: KindAudit, Project: "proj", Tier: credit.PrefFast,
	})
	if err != nil {
		t.Fatalf("Enqueue with a tier: %v", err)
	}
	if chosen.Tier != credit.PrefFast {
		t.Errorf("Enqueue returned tier %q, want %q", chosen.Tier, credit.PrefFast)
	}
	if got, _ := s.Get(ctx, chosen.ID); got.Tier != credit.PrefFast {
		t.Errorf("tier read back as %q, want %q — the choice did not survive the store", got.Tier, credit.PrefFast)
	}

	// Omitting the tier stores nothing rather than writing a default. Unset is
	// the state of a session that predates the choice, and OrDefault is the
	// single place it resolves.
	unchosen, err := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "untiered", Kind: KindAudit, Project: "proj"})
	if err != nil {
		t.Fatalf("Enqueue without a tier: %v", err)
	}
	if got, _ := s.Get(ctx, unchosen.ID); got.Tier != "" {
		t.Errorf("an unchosen tier was stored as %q; unset must stay unset", got.Tier)
	} else if got.Tier.OrDefault() != credit.PrefBest {
		t.Errorf("an unchosen session resolves to %q, want %q", got.Tier.OrDefault(), credit.PrefBest)
	}
}

// runGuardContract asserts the double-trigger guard both stores must enforce:
// a non-terminal session for a paper blocks a second one (in the project or
// anywhere the principal owns), a deliberate re-run after the prior finished is
// allowed, a different paper is never blocked, and an empty key never guards.
func runGuardContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	const key = "arxiv:2302.03714"

	// First session for the paper opens.
	first, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: key})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	// A second session for the same paper in the same project is refused: the
	// accidental duplicate.
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: key}); !errors.Is(err, ErrActiveSession) {
		t.Errorf("same project double-fire: want ErrActiveSession, got %v", err)
	}
	// The same paper in a different project the same principal owns is also
	// refused, by the per-principal guard that covers a submit outside a project.
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projB", PaperKey: key}); !errors.Is(err, ErrActiveSession) {
		t.Errorf("cross-project double-fire: want ErrActiveSession, got %v", err)
	}
	// A different paper in the same project is never blocked.
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: "arxiv:9999.99999"}); err != nil {
		t.Errorf("different paper blocked: %v", err)
	}
	// An empty key, from an experiment or a ref that cannot be keyed, is never
	// guarded.
	for i := range 2 {
		if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "exp", Kind: KindExperiment, Project: "projA"}); err != nil {
			t.Errorf("empty-key enqueue %d blocked: %v", i, err)
		}
	}

	// Finish the first session; a re-run of the same paper is now allowed.
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, first.ID, p, nil); err != nil {
			t.Fatalf("advance first to %s: %v", p, err)
		}
	}
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: key}); err != nil {
		t.Errorf("re-run after done blocked: %v", err)
	}

	// Cancelling a session frees the guard slot immediately, because cancel
	// writes the terminal 'failed', so the same paper can be stopped and
	// resubmitted. This is checked against the partial index as well as the
	// in-memory check.
	const key2 = "arxiv:1111.11111"
	c1, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projC", PaperKey: key2})
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projC", PaperKey: key2}); !errors.Is(err, ErrActiveSession) {
		t.Errorf("pre-cancel double-fire: want ErrActiveSession, got %v", err)
	}
	got, err := s.Cancel(ctx, c1.ID)
	if err != nil || got.Phase != PhaseFailed || got.Err != CancelCause {
		t.Fatalf("Cancel = %+v err=%v; want failed/cancelled", got, err)
	}
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projC", PaperKey: key2}); err != nil {
		t.Errorf("resubmit after cancel blocked: %v", err)
	}
	// Cancelling an already-terminal session is a no-op, not an error.
	if again, err := s.Cancel(ctx, c1.ID); err != nil || again.Phase != PhaseFailed {
		t.Errorf("idempotent cancel: %+v err=%v", again, err)
	}
}

// runProjectJoinContract asserts the two project-join reads both stores must
// hold: sessions scoped to one project, newest-first, and per-project counts
// for one owner in a single call.
func runProjectJoinContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	_, _ = s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-1", Kind: KindAudit, Project: "pX"})
	_, _ = s.Enqueue(ctx, Submission{Principal: "stranger", Ref: "ref-4", Kind: KindAudit, Project: "pX"})
	_, _ = s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-3", Kind: KindAudit, Project: "pY"})
	two, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-2", Kind: KindAudit, Project: "pX"}) // newest in pX

	inX, err := s.ListByProject(ctx, "pX")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	// pX holds both owner sessions and the stranger's, newest-first.
	if len(inX) != 3 || inX[0].ID != two.ID {
		t.Fatalf("pX sessions = %d, first %s; want 3 newest-first %s", len(inX), first3(inX), two.ID)
	}

	status, err := s.StatusByProjects(ctx, []string{"pX", "pY"})
	if err != nil {
		t.Fatalf("StatusByProjects: %v", err)
	}
	// Every session in the project counts, whoever submitted it: the stranger's
	// run in pX belongs to pX (018). All four are still submitted, so all four
	// are running.
	if status["pX"].Total != 3 || status["pY"].Total != 1 {
		t.Errorf("status = %v; want pX total 3 (including the stranger), pY total 1", status)
	}
	if status["pX"].Running != 3 || status["pX"].Latest != PhaseSubmitted {
		t.Errorf("pX status = %+v; want running 3, latest submitted", status["pX"])
	}
	if status["pX"].LatestAt.IsZero() {
		t.Error("pX LatestAt is zero; want the newest session's update time")
	}
	// A project not asked about is not summarized, so a list pays only for the
	// page it shows.
	if _, ok := status["pZ"]; ok {
		t.Error("pZ summarized; want only the projects asked for")
	}

	runSpendWindowContract(t, s)
}

// runSpendWindowContract asserts the window both stores must agree on (021e).
// The edges matter: the window is on when a session last changed, so a long
// run that settled inside it counts and a short one that settled before it
// does not.
func runSpendWindowContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// Every session enqueued above updated just now, so a window opening in the
	// past holds all of them and one opening in the future holds none. That is
	// the assertion that catches a store filtering on created_at, or on the
	// wrong side of the comparison.
	all, err := s.SpendSince(ctx, []string{"pX", "pY"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	none, err := s.SpendSince(ctx, []string{"pX", "pY"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a window that has not opened yet reported spend: %v", none)
	}
	// Nothing has run, so nothing has been spent, but the projects inside the
	// window must be summable without error and must not report a non-zero
	// total.
	for _, id := range []string{"pX", "pY"} {
		if all[id] != 0 {
			t.Errorf("%s spent %d before any session ran", id, all[id])
		}
	}

	// Cost has to reach the total: usage against the session's rate card,
	// through the same withCost the row rendering uses. Without this the whole
	// contract compares 0 with 0 and a store that summed nothing would pass.
	spent, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-paid", Kind: KindAudit, Project: "pZ"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	before := time.Now()
	if err := s.Observe(ctx, spent.ID, provider.RunStatus{Usage: provider.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	paid, err := s.SpendSince(ctx, []string{"pZ"}, before.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if paid["pZ"] <= 0 {
		t.Fatalf("a session that burned two million tokens summed to %d; usage is not reaching the total", paid["pZ"])
	}

	// The window is on when a session last changed, not when it started. This
	// is the assertion a store filtering on created_at fails: the session was
	// created before mid and updated after it, so it must still be counted.
	mid := time.Now()
	if err := s.Advance(ctx, spent.ID, PhaseResolving, nil); err != nil {
		t.Fatalf("advance: %v", err)
	}
	late, err := s.SpendSince(ctx, []string{"pZ"}, mid)
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if late["pZ"] != paid["pZ"] {
		t.Errorf("a session created before the window and updated inside it summed to %d, want %d — the filter is on created_at, not updated_at", late["pZ"], paid["pZ"])
	}

	// A project nobody asked about is never summed.
	if _, ok := all["pZ"]; ok {
		t.Error("pZ summed; want only the projects asked for")
	}
	if got, err := s.SpendSince(ctx, nil, time.Now().Add(-24*time.Hour)); err != nil || len(got) != 0 {
		t.Errorf("SpendSince(nil) = %v, %v; want an empty map and no error", got, err)
	}
}

// runDeleteContract asserts what a soft-deleted session must and must not do
// (032). The four hiding read paths are asserted one by one, because the bug
// this guards against is a store that filters three of them and leaves the
// fourth returning a deleted session, and the fourth is the one publishing
// reads.
//
// The asymmetry is asserted too: SpendSince keeps counting, because the ledger
// still holds the charge.
func runDeleteContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	kept, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "keep", Kind: KindAudit, Project: "pD"})
	if err != nil {
		t.Fatalf("enqueue keep: %v", err)
	}
	gone, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "gone", Kind: KindAudit, Project: "pD"})
	if err != nil {
		t.Fatalf("enqueue gone: %v", err)
	}
	// Only a terminal session is deletable in the product, so move it there. The
	// spend assertion below needs it settled anyway.
	if err := s.Advance(ctx, gone.ID, PhaseFailed, func(j *Job) { j.Err = "publish failed" }); err != nil {
		t.Fatalf("advance gone to failed: %v", err)
	}

	before, err := s.SpendSince(ctx, []string{"pD"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SpendSince before: %v", err)
	}

	if err := s.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := s.Get(ctx, gone.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted) = %v; want ErrNotFound — the gate every per-session read resolves through", err)
	}
	if _, err := s.Get(ctx, kept.ID); err != nil {
		t.Errorf("Get(kept) = %v; want the surviving session", err)
	}
	byProject, err := s.ListByProject(ctx, "pD")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(byProject) != 1 || byProject[0].ID != kept.ID {
		t.Errorf("ListByProject = %s; want only %s — this is the list publishing reads", first3(byProject), kept.ID)
	}
	byPrincipal, err := s.ListByPrincipal(ctx, "owner")
	if err != nil {
		t.Fatalf("ListByPrincipal: %v", err)
	}
	for _, j := range byPrincipal {
		if j.ID == gone.ID {
			t.Errorf("ListByPrincipal still holds %s; want it hidden", gone.ID)
		}
	}
	status, err := s.StatusByProjects(ctx, []string{"pD"})
	if err != nil {
		t.Fatalf("StatusByProjects: %v", err)
	}
	if status["pD"].Total != 1 {
		t.Errorf("pD total = %d; want 1 — a deleted session is not counted", status["pD"].Total)
	}

	// The one read that keeps it: deleting a session does not undo its spend.
	after, err := s.SpendSince(ctx, []string{"pD"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SpendSince after: %v", err)
	}
	if after["pD"] != before["pD"] {
		t.Errorf("spend %d -> %d; want unchanged, the ledger still holds the charge", before["pD"], after["pD"])
	}

	// Delete is idempotent, and succeeds on an id that was never stored.
	if err := s.Delete(ctx, gone.ID); err != nil {
		t.Errorf("second Delete: %v; want a no-op", err)
	}
	if err := s.Delete(ctx, "no-such-session"); err != nil {
		t.Errorf("Delete(missing): %v; want a success", err)
	}

	// The 019 purge removes the deleted row with the rest, so a purged project
	// leaves nothing behind.
	if err := s.DeleteByProject(ctx, "pD"); err != nil {
		t.Fatalf("DeleteByProject: %v", err)
	}
	if left, err := s.ListByProject(ctx, "pD"); err != nil || len(left) != 0 {
		t.Errorf("after purge = %s, %v; want nothing", first3(left), err)
	}
}

// first3 renders up to three ids, for a failure message.
func first3(js []Job) string {
	var ids []string
	for i, j := range js {
		if i == 3 {
			break
		}
		ids = append(ids, j.ID)
	}
	return strings.Join(ids, ",")
}

// runClaimContention asserts that concurrent claims are disjoint. The assertion
// only matters against Postgres's SKIP LOCKED.
func runClaimContention(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	const n = 30
	for range n {
		if _, err := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if j, ok, err := s.Claim(ctx, "test-worker"); err == nil && ok {
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("job %s claimed %d times", id, c)
		}
	}
}

// runLeaseContract asserts that a lease tracks whether a driver is still alive,
// with time under the test's control: a live driver keeps its run, and a driver
// that went quiet loses it. at sets the store's clock.
func runLeaseContract(t *testing.T, s Store, at func(time.Time)) {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	at(start)

	j, err := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "leased", Kind: KindAudit})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if won, err := s.Adopt(ctx, j.ID, "pod-a", time.Minute); err != nil || !won {
		t.Fatalf("Adopt = %v/%v, want it won", won, err)
	}

	// An observation reports that the driver is still alive. It arrives on
	// every poll of the backend, so a run that takes hours keeps its lease
	// without a separate heartbeat, and a sweep looking for stranded work
	// leaves it alone.
	at(start.Add(45 * time.Second))
	if err := s.Observe(ctx, j.ID, provider.RunStatus{State: provider.StateRunning}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	at(start.Add(90 * time.Second))
	if won, err := s.Adopt(ctx, j.ID, "pod-b", time.Minute); err != nil || won {
		t.Fatalf("Adopt = %v/%v, want it refused — pod-a is still observing", won, err)
	}

	// Once the observations stop, the ttl runs out and the run is adoptable: a
	// driver that died must not keep a session indefinitely.
	at(start.Add(3 * time.Minute))
	if won, err := s.Adopt(ctx, j.ID, "pod-b", time.Minute); err != nil || !won {
		t.Fatalf("Adopt = %v/%v, want it won — pod-a went quiet", won, err)
	}

	// Observing a job nobody leased does not lease it.
	free, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "unleased", Kind: KindAudit})
	if err := s.Observe(ctx, free.ID, provider.RunStatus{State: provider.StateRunning}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if won, err := s.Adopt(ctx, free.ID, "pod-c", time.Minute); err != nil || !won {
		t.Fatalf("Adopt of an unleased job = %v/%v, want it won", won, err)
	}
}

// runImportContract asserts the ownership rule for imported records, which both
// stores hold: a re-push finds the pusher's own row and nobody else's, and
// never moves a record between projects.
func runImportContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	record := func(principal, project string, phase Phase) Import {
		return Import{
			Principal: principal, Project: project, Ref: "arxiv:2302.03714", PaperKey: "arxiv:2302.03714",
			Runtime: "local", ExternalID: "7f3a91c2", Phase: phase,
			Status: provider.RunStatus{State: provider.StateDone, Runtime: "local", RunID: "7f3a91c2"},
		}
	}

	first, err := s.ImportLocal(ctx, record("alice", "P", PhaseFailed))
	if err != nil {
		t.Fatalf("ImportLocal: %v", err)
	}
	// Alice re-pushes after a retry: her row, updated in place.
	again, err := s.ImportLocal(ctx, record("alice", "P", PhaseDone))
	if err != nil || again.ID != first.ID || again.Phase != PhaseDone {
		t.Fatalf("re-push = %+v, %v; want the same row moved to done", again, err)
	}

	// Bob, a member of another project, pushes the same session id: he gets his
	// own row, and Alice's is unchanged.
	bobs, err := s.ImportLocal(ctx, record("bob", "Q", PhaseDone))
	if err != nil {
		t.Fatalf("another principal's import: %v", err)
	}
	if bobs.ID == first.ID {
		t.Fatal("another principal's push landed on Alice's row")
	}
	alices, _ := s.Get(ctx, first.ID)
	if alices.ProjectID != "P" || alices.Principal != "alice" {
		t.Fatalf("Alice's row after Bob's push = project %s principal %s; it was re-homed", alices.ProjectID, alices.Principal)
	}

	// Alice pushes her own session under a different project: the push is
	// refused and the record stays where it was. A record is moved through the
	// app.
	if _, err := s.ImportLocal(ctx, record("alice", "R", PhaseDone)); !errors.Is(err, ErrImportedElsewhere) {
		t.Fatalf("re-push into another project: err=%v, want ErrImportedElsewhere", err)
	}
	alices, _ = s.Get(ctx, first.ID)
	if alices.ProjectID != "P" {
		t.Fatalf("Alice's row moved to %s", alices.ProjectID)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	runImportContract(t, NewMemStore())
	runStoreContract(t, NewMemStore())
	runGuardContract(t, NewMemStore())
	runProjectJoinContract(t, NewMemStore(WithRates(fixedCard(testCard))))
	runDeleteContract(t, NewMemStore(WithRates(fixedCard(testCard))))
	runClaimContention(t, NewMemStore())
	memNow := time.Now()
	runLeaseContract(t, NewMemStore(WithClock(func() time.Time { return memNow })), func(at time.Time) { memNow = at })
}

// TestPGStoreSatisfiesTheContract runs the same contract against a real
// Postgres, skipping when TEST_DATABASE_URL is not set. `docker compose` sets
// it, so the SKIP LOCKED path is exercised against a real database.
func TestPGStoreSatisfiesTheContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres store contract")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	runStoreContract(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runGuardContract(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Priced like the in-memory side: the join contract ends in the spend
	// window, which asserts that usage reaches the project total. A store with
	// no card prices every session at 0 and the assertion could never hold.
	runProjectJoinContract(t, NewPGStore(pool, WithPGRates(fixedCard(testCard))))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runDeleteContract(t, NewPGStore(pool, WithPGRates(fixedCard(testCard))))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runClaimContention(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runImportContract(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	pgNow := time.Now()
	runLeaseContract(t, NewPGStore(pool, WithPGClock(func() time.Time { return pgNow })), func(at time.Time) { pgNow = at })
}

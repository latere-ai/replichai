import Machines.Job

/-!
Claims about the session phase table, `job.Phases` in internal/job, written by
hand. Each is a finite check over the generated table, closed by `decide`,
carried to every path by a lemma of `Statemachine`. A change to the table that
breaks a claim fails the build.
-/

namespace Proofs.Job
open Machines.Job Statemachine

/-- A session is done only by publishing: no other phase leads to `done`. -/
theorem done_only_from_publishing :
    ∀ s ∈ machine.states, State.done ∈ machine.next s → s = .publishing := by
  decide

/-- Every phase a session can still leave can fail, so a cancel, a lost run
or a restart always has a step to take. Lease.tla relies on it. -/
theorem every_active_phase_can_fail :
    ∀ s ∈ machine.states, machine.next s ≠ [] → State.failed ∈ machine.next s := by
  decide

/-- A finished session never runs again. -/
theorem finished_stays (a s : State) (ha : a = .done ∨ a = .failed)
    (r : Reaches machine a s) : s = a := by
  rcases ha with rfl | rfl
  · exact machine.terminal_stays terminal_done r
  · exact machine.terminal_stays terminal_failed r

/-- Once publishing, a session is never resolving or running again. -/
theorem publishing_does_not_go_back (s : State) (r : Reaches machine .publishing s) :
    s ∈ [State.publishing, .done, .failed] :=
  machine.reaches_closed (by decide) r (by decide)

/-- Concurrent writers that check the phase they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Job

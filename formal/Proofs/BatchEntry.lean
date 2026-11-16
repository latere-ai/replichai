import Machines.BatchEntry

/-!
Claims about one paper's entry in a batch run, `batch.Entries` in
internal/batch, written by hand.
-/

namespace Proofs.BatchEntry
open Machines.BatchEntry Statemachine

/-- An entry is interrupted only while running: one a dead process left
running is read back as interrupted, and nothing else is. -/
theorem interrupted_only_from_running :
    ∀ s ∈ machine.states, State.interrupted ∈ machine.next s → s = .running := by
  decide

/-- An entry runs before it ends: pending leads only to running. -/
theorem pending_only_starts : machine.next .pending = [.running] := by decide

/-- A finished entry stays finished. A resume runs an interrupted paper again
as a new entry, never by moving the old one. -/
theorem finished_stays (a s : State) (ha : machine.Terminal a)
    (r : Reaches machine a s) : s = a :=
  machine.terminal_stays ha r

/-- Concurrent writers that check the state they land on keep the table. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.BatchEntry

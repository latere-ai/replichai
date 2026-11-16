import Machines.RuntimeSession

/-!
Claims about a runtime session's state, `managed.Sessions` in
internal/managed, written by hand. Every runtime reports its sessions in these
states, and the controller stops polling and releases a session once it
reaches a final one.
-/

namespace Proofs.RuntimeSession
open Machines.RuntimeSession Statemachine

/-- A finished session is never reported running again, so a late poll or a
late cancel cannot reopen it. -/
theorem finished_stays (a s : State) (ha : a = .done ∨ a = .failed)
    (r : Reaches machine a s) : s = a := by
  rcases ha with rfl | rfl
  · exact machine.terminal_stays terminal_done r
  · exact machine.terminal_stays terminal_failed r

/-- A running session never goes back to pending. -/
theorem running_does_not_go_back (s : State) (r : Reaches machine .running s) :
    s ∈ [State.running, .done, .failed] :=
  machine.reaches_closed (by decide) r (by decide)

/-- Every live state can finish either way, so a cancel always has a step. -/
theorem live_can_finish :
    ∀ s ∈ machine.states, machine.next s ≠ [] →
      State.done ∈ machine.next s ∧ State.failed ∈ machine.next s := by
  decide

/-- Concurrent writers that check the state they land on keep the table. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.RuntimeSession

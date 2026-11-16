import Machines.Grant

/-!
Claims about the access grant table, `access.Grants` in internal/access,
written by hand. Each is a finite check over the generated table, closed by
`decide`, carried to every path by a lemma of `Statemachine`.
-/

namespace Proofs.Grant
open Machines.Grant Statemachine

/-- Access can always be taken away: every state other than revoked has a
step to revoked. -/
theorem always_revocable :
    ∀ s ∈ machine.states, s ≠ .revoked → State.revoked ∈ machine.next s := by
  decide

/-- A person is bound only from an invitation or a re-grant of a revoked
row, never from nowhere. -/
theorem bound_only_from_invited_or_revoked :
    ∀ s ∈ machine.states, State.bound ∈ machine.next s → s = .invited ∨ s = .revoked := by
  decide

/-- No grant is final: a revoked address can be invited again, so a
revocation is a state, not a deletion. -/
theorem no_final_state : ∀ s ∈ machine.states, machine.next s ≠ [] := by
  decide

/-- Concurrent writers that check the state they land on keep the table. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Grant

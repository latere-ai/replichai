import Machines.Publication

/-!
Claims about a project's publication state, `project.Publications` in
internal/project, written by hand. PublishGate.tla adds what one table cannot
say: no session runs in a project that is not private.
-/

namespace Proofs.Publication
open Machines.Publication Statemachine

/-- A project is published only from private: a project in the trash is
restored before it can be published. -/
theorem public_only_from_private :
    ∀ s ∈ machine.states, State.public ∈ machine.next s → s = .private := by
  decide

/-- A project goes to the trash only from private: a published project is
withdrawn first, which records the withdrawal. -/
theorem trashed_only_from_private :
    ∀ s ∈ machine.states, State.trashed ∈ machine.next s → s = .private := by
  decide

/-- Every state can return to private, so publishing and trashing are both
undoable. -/
theorem private_always_reachable :
    ∀ s ∈ machine.states, s = .private ∨ State.private ∈ machine.next s := by
  decide

/-- Concurrent writers that check the state they land on keep the table. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Publication

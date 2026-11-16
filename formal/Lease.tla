---------------------------- MODULE Lease ----------------------------
(* Who may move a session's phase when several server replicas run, as     *)
(* internal/job implements it.                                              *)
(*                                                                          *)
(* The jobs row holds the phase and the lease: the replica that drives the  *)
(* session (adopted_by) and whether its heartbeat is recent (adopted_at     *)
(* within the ttl). Each replica also keeps its own belief that it drives   *)
(* the session, which can be stale: a replica that stalled past the ttl     *)
(* still believes it drives until its next heartbeat says otherwise.        *)
(*                                                                          *)
(* Writers of the phase:                                                    *)
(*   Claim    submitted, unowned: resolving, owned by the claimer.          *)
(*   Advance  the row's owner is the writer, the step is in the table.      *)
(*   Cancel   any replica, any active phase: failed. No lease, by design:   *)
(*            a person's stop must land whoever drives the session.         *)
(* Writers of the lease:                                                    *)
(*   Touch    the owner renews; a replica that is not the owner of an       *)
(*            active row stops driving it and cancels its local run.        *)
(*   Adopt    an active row past submitted whose lease is stale or unset    *)
(*            changes owner. The sweep and start-up use it to take over or  *)
(*            fail a session whose driver is gone.                          *)
(*                                                                          *)
(* OwnerCheck = TRUE is the rule that Advance lands only when the writer is *)
(* the owner in the locked row. The phases and their successors come from   *)
(* the generated Job module, so this model follows the Go table.            *)
EXTENDS Naturals

CONSTANTS Replicas, OwnerCheck

VARIABLES phase, owner, fresh, drives, strayWrite
vars == <<phase, owner, fresh, drives, strayWrite>>

J == INSTANCE Job WITH States <- {}, Initial <- {}, Succ <- [s \in {} |-> {}],
                       Writers <- {}, Guarded <- TRUE,
                       status <- phase, seen <- [w \in {} |-> "-"]

Phases == J!MCStates
Succ == J!MCSucc
Active == {s \in Phases : Succ[s] /= {}}
Final == Phases \ Active
Submitted == "submitted"
Resolving == "resolving"
Failed == "failed"
None == "none"

ASSUME Resolving \in Succ[Submitted] /\ \A s \in Active : Failed \in Succ[s]

Init == /\ phase \in J!MCInitial
        /\ owner = None
        /\ fresh = FALSE
        /\ drives = [r \in Replicas |-> FALSE]
        /\ strayWrite = FALSE

Claim(r) == /\ phase = Submitted
            /\ owner = None
            /\ phase' = Resolving
            /\ owner' = r
            /\ fresh' = TRUE
            /\ drives' = [drives EXCEPT ![r] = TRUE]
            /\ UNCHANGED strayWrite

(* Time passes without a heartbeat. *)
Expire == /\ fresh
          /\ fresh' = FALSE
          /\ UNCHANGED <<phase, owner, drives, strayWrite>>

Touch(r) == /\ drives[r]
            /\ IF owner = r /\ phase \in Active
                 THEN /\ fresh' = TRUE
                      /\ UNCHANGED drives
                 ELSE /\ drives' = [drives EXCEPT ![r] = FALSE]
                      /\ UNCHANGED fresh
            /\ UNCHANGED <<phase, owner, strayWrite>>

Adopt(r) == /\ phase \in Active \ {Submitted}
            /\ owner /= r
            /\ owner = None \/ ~fresh
            /\ owner' = r
            /\ fresh' = TRUE
            /\ drives' = [drives EXCEPT ![r] = TRUE]
            /\ UNCHANGED <<phase, strayWrite>>

Cancel == /\ phase \in Active
          /\ phase' = Failed
          /\ UNCHANGED <<owner, fresh, drives, strayWrite>>

Advance(r) == /\ drives[r]
              /\ OwnerCheck => owner = r
              /\ \E t \in Succ[phase] : phase' = t
              /\ strayWrite' = (strayWrite \/ owner /= r)
              /\ UNCHANGED <<owner, fresh, drives>>

(* A finished session that no replica drives has nothing left to do. *)
Finished == /\ phase \in Final
            /\ \A r \in Replicas : ~drives[r]
            /\ UNCHANGED vars

Next == \/ Expire \/ Cancel \/ Finished
        \/ \E r \in Replicas : Claim(r) \/ Touch(r) \/ Adopt(r) \/ Advance(r)

Fairness == \A r \in Replicas : WF_vars(Touch(r))

Spec == Init /\ [][Next]_vars /\ Fairness

TypeOK == /\ phase \in Phases
          /\ owner \in Replicas \cup {None}
          /\ fresh \in BOOLEAN
          /\ drives \in [Replicas -> BOOLEAN]
          /\ strayWrite \in BOOLEAN

(* Advance lands only for the replica the row names as its owner. *)
OnlyOwnerWrites == ~strayWrite

(* Every phase change, by any writer, is a step of the Job table. *)
FollowsTable == [][phase' \in Succ[phase]]_phase

(* A finished session stays finished. *)
FinalStays == [][(phase \in Final) => (phase' = phase)]_phase

(* Once a session finishes, canceled on any replica or not, every replica *)
(* stops driving it.                                                      *)
DriversStop == (phase \in Final) ~> (\A r \in Replicas : ~drives[r])
=============================================================================

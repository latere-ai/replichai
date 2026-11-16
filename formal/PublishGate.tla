---------------------------- MODULE PublishGate ----------------------------
(* A published or trashed project runs no session.                          *)
(*                                                                          *)
(* Starting a session checks that the project is private and inserts the    *)
(* session; publishing and trashing check that no session is active and     *)
(* change the visibility. Locked = TRUE is the rule in internal/job and     *)
(* internal/project: the start reads the project row FOR SHARE and inserts  *)
(* in one transaction, and publish and trash lock the row FOR UPDATE and    *)
(* count active sessions in theirs, so each runs as one step. With          *)
(* Locked = FALSE the start's check and its insert are separate steps. The  *)
(* visibilities and their successors come from the generated Publication    *)
(* module.                                                                  *)
EXTENDS Naturals

CONSTANTS Starters, MaxActive, Locked

VARIABLES vis, active, checked
vars == <<vis, active, checked>>

P == INSTANCE Publication WITH States <- {}, Initial <- {}, Succ <- [s \in {} |-> {}],
                               Writers <- {}, Guarded <- TRUE,
                               status <- vis, seen <- [w \in {} |-> "-"]

Succ == P!MCSucc
Private == "private"

Init == /\ vis \in P!MCInitial
        /\ active = 0
        /\ checked = [s \in Starters |-> FALSE]

(* A start that holds the row: check and insert in one step. *)
StartLocked(s) == /\ Locked
                  /\ vis = Private
                  /\ active < MaxActive
                  /\ active' = active + 1
                  /\ UNCHANGED <<vis, checked>>

(* A start that does not: the check, then later the insert. *)
StartCheck(s) == /\ ~Locked
                 /\ ~checked[s]
                 /\ vis = Private
                 /\ checked' = [checked EXCEPT ![s] = TRUE]
                 /\ UNCHANGED <<vis, active>>

StartInsert(s) == /\ checked[s]
                  /\ active < MaxActive
                  /\ active' = active + 1
                  /\ checked' = [checked EXCEPT ![s] = FALSE]
                  /\ UNCHANGED vis

Finish == /\ active > 0
          /\ active' = active - 1
          /\ UNCHANGED <<vis, checked>>

(* Publish or trash leave private only with no session active; withdraw and *)
(* restore return to private.                                               *)
Move == \E t \in Succ[vis] :
          /\ (vis = Private) => (active = 0)
          /\ vis' = t
          /\ UNCHANGED <<active, checked>>

Next == \/ Finish \/ Move
        \/ \E s \in Starters : StartLocked(s) \/ StartCheck(s) \/ StartInsert(s)

Spec == Init /\ [][Next]_vars

TypeOK == /\ vis \in P!MCStates
          /\ active \in 0..MaxActive
          /\ checked \in [Starters -> BOOLEAN]

(* No session runs in a project that is not private. *)
Quiet == (vis /= Private) => (active = 0)

FollowsTable == [][vis' \in Succ[vis]]_vis
=============================================================================

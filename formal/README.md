# Formal models

ReplicHAI keeps every stored lifecycle as a table in Go, declared with
[`latere.ai/x/pkg/statemachine`](https://github.com/latere-ai/pkg/tree/main/statemachine).
This directory holds those tables rendered into Lean 4 and TLA+, the claims
proved or model-checked about them, and the protocols that span more than one
table. The `lean` and `tla` gates of `go tool lateregate` check all of it on
every change. How the package keeps code and models in step is in its
[README](https://github.com/latere-ai/pkg/blob/main/statemachine/README.md);
this file says what is here and which Go test holds each piece to the code.

## Rendered from the tables

`TestFormalModels` in [`internal/formal`](../internal/formal/formal_test.go)
renders these, and fails when a table changed without them. Never edit them by
hand: change the table, then run

```sh
go test ./internal/formal -run TestFormalModels -update
```

| Table | Go | Lean | TLA+ |
|---|---|---|---|
| Job, a session's phase | `job.Phases` | `Machines/Job.lean` | `Job.tla`, `Job.cfg`, `Job.stale.cfg` |
| Grant, a person's access | `access.Grants` | `Machines/Grant.lean` | `Grant.tla`, `Grant.cfg`, `Grant.stale.cfg` |
| Publication, a project's visibility | `project.Publications` | `Machines/Publication.lean` | `Publication.tla`, `Publication.cfg`, `Publication.stale.cfg` |
| RuntimeSession, a runtime's view of a session | `managed.Sessions` | `Machines/RuntimeSession.lean` | `RuntimeSession.tla`, `RuntimeSession.cfg`, `RuntimeSession.stale.cfg` |
| BatchEntry, one paper in a batch run | `batch.Entries` | `Machines/BatchEntry.lean` | `BatchEntry.tla`, `BatchEntry.cfg`, `BatchEntry.stale.cfg` |

`Statemachine.lean`, `Transition.tla` and the Lake files are the shared
semantics and project, rendered the same way. Each `<Table>.cfg` checks that
writers which check the state they land on only take the table's steps; each
`<Table>.stale.cfg` checks that writers which check a state read earlier do
leave the table, so the gate fails if that trace stops being found.

## Written by hand

| File | Claim | Held to the code by |
|---|---|---|
| `Proofs/Job.lean` | done only follows publishing; every active phase can fail; a finished session never runs again; concurrent guarded writers keep the table | the rendered `Machines/Job.lean`, which `TestFormalModels` keeps equal to `job.Phases` |
| `Proofs/Grant.lean` | access can always be revoked; a person is bound only from an invitation or a re-grant; no grant state is final | the rendered `Machines/Grant.lean` |
| `Proofs/Publication.lean` | a project is published or trashed only from private; every state can return to private | the rendered `Machines/Publication.lean` |
| `Proofs/RuntimeSession.lean` | a finished session is never reported running again; a running one never goes back to pending; a live session can always finish either way | the rendered `Machines/RuntimeSession.lean` |
| `Proofs/BatchEntry.lean` | an entry is interrupted only while running; a finished entry stays finished | the rendered `Machines/BatchEntry.lean` |
| `Proofs/Refund.lean` | refunds of one purchase, arriving in any order, never take back more than it credited, and a full refund takes back exactly that | `TestRefundReversalMatchesTheBigIntegerRule` and `TestRefundReversalOrderDoesNotChangeTheTotal` in `internal/credit`, which check `credit.RefundReversal` against the same formula over random inputs |
| `Lease.tla` | only the replica that holds a session's lease moves its phase, every move is a step of the Job table, and when a session finishes every replica stops driving it. `Lease.unchecked.cfg` shows a stalled replica writing when the owner check is removed | the lease contract tests in `internal/job` (`runLeaseWriteContract`, `TestAReplicaThatLostASessionDoesNotFinishIt`, `TestACancelOnAnotherReplicaStopsTheRun`); it reads its phases from the rendered `Job.tla` |

| `PublishGate.tla` | no session runs in a project that is published or in the trash. `PublishGate.unlocked.cfg` shows a session landing on a published project when starting a session checks the visibility and inserts in separate steps | `TestPGStoreRefusesAPublicOrTrashedProject` in `internal/job` and `TestPGStoreRefusesPublishAndTrashWithASessionInFlight` in `internal/project`; it reads its visibilities from the rendered `Publication.tla` |

A protocol model's rules are restated from the Go code, so a change to how
`internal/job` takes, renews or checks a lease must change `Lease.tla`, and a
change to how a session start or a publish locks the project row must change
`PublishGate.tla`, in the same commit. The tests named above are what fail first when the two disagree.

## Checking locally

```sh
go tool lateregate lean   # lake build --wfail: a sorry or a warning fails
go tool lateregate tla    # TLC on every .cfg with its .tla
```

The `lean` gate installs Lean through elan when `lake` is missing. The `tla`
gate needs Java (`brew install openjdk` on macOS) and downloads a pinned
`tla2tools.jar` into the user cache.

# 014c — Publication requires evidence

**Status:** designed.

**Scope:** make server and CLI finalization stop when the paper identity or a named managed session's transcript cannot be read. The publication gate must never call missing evidence "clear."

**Depends on:** [002](002-publishing-reproductions.md), [005](005-transcript.md), [006i](006i-server-side-mount-and-publish.md), [009](009-secret-containment-when-the-agent-commits-live.md).

## Problem

Server publishing ignores errors reading the recorded paper identity, ignores fallback resolution errors, and can commit a manifest whose identity is empty. It also converts transcript retrieval failure into an empty transcript; `FinalizeLive` then sees no transcript leak and records a clear redistribution review. The CLI finish path does the same. Separately, transcript rendering silently drops an event that cannot be JSON encoded while the index still claims the full count.

## Decisions

**Absence permits fallback; errors do not.** A clean `ok=false` from `RecordedIdentity` invokes paper resolution. A forge read error is returned. Fallback resolution must succeed and produce a non-empty identity before finalization.

**A named session promises evidence.** When a run/session id exists, transcript retrieval failure is a publish failure. The worker remains in `publishing`, so reconciliation retries the idempotent finalize. CLI `finish --session` returns the same failure. Finalizing without a session remains supported for the explicit manual-verdict path.

**Canonical transcript rendering is all-or-error.** `RenderTranscriptFiles` returns an error with the event index when JSON encoding fails; no shorter JSONL is committed beside an index claiming more events.

**CLI overrides are validated.** An invalid `--verdict` is returned before any forge write instead of silently becoming `unknown`.

## Acceptance criteria

1. Recorded-identity transport/decode fallback failures commit no manifest; clean absence resolves and finalizes as before.
2. Transcript retrieval failure with a run id commits neither a clear manifest nor an incomplete transcript and remains retryable.
3. One unencodable event fails transcript rendering and finalization without partial transcript files.
4. An invalid CLI verdict fails before publication.

## Tests

- Runner forge tests inject recorded-identity and resolver failures and assert no manifest write.
- Server and CLI transcript-provider tests inject failure and assert finalization stops.
- Publish tests use invalid `json.RawMessage` and assert the failing event index.
- CLI tests cover an invalid `--verdict`.


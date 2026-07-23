"""An in-process provider that runs nothing.

Useful for exercising the orchestrator, for tests, and for demonstrating the
contract a real adapter has to satisfy. It records every call it receives and
returns canned artifacts after a configurable number of polls.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from replichai.model import (
    Artifact,
    Grade,
    RunHandle,
    RunSpec,
    RunState,
    RunStatus,
    Verdict,
    WorkerRef,
    WorkerSpec,
    WorkspaceRef,
    WorkspaceSpec,
)
from replichai.providers.base import Provider


@dataclass
class MemoryProvider(Provider):
    """Deterministic stand-in for a real backend."""

    name: str = "memory"
    model: str = "memory-model"
    polls_before_done: int = 1
    grade: Grade = Grade.PASSED
    files: dict[str, bytes] = field(default_factory=dict)

    started: list[RunSpec] = field(default_factory=list)
    workers: list[WorkerSpec] = field(default_factory=list)
    workspaces: list[WorkspaceSpec] = field(default_factory=list)
    closed: list[str] = field(default_factory=list)
    _polls: dict[str, int] = field(default_factory=dict)
    _counter: int = 0

    def resolve_model(self, preference: str) -> str:
        return self.model

    def ensure_workspace(self, spec: WorkspaceSpec) -> WorkspaceRef:
        self.workspaces.append(spec)
        return WorkspaceRef(id=f"workspace-{len(self.workspaces)}")

    def ensure_worker(self, spec: WorkerSpec) -> WorkerRef:
        self.workers.append(spec)
        return WorkerRef(id=f"worker-{len(self.workers)}", revision=1)

    def start_run(
        self, worker: WorkerRef, workspace: WorkspaceRef, spec: RunSpec
    ) -> RunHandle:
        self.started.append(spec)
        self._counter += 1
        handle = RunHandle(id=f"run-{self._counter}", worker=worker, workspace=workspace)
        self._polls[handle.id] = 0
        return handle

    def poll(self, handle: RunHandle) -> RunStatus:
        count = self._polls.get(handle.id, 0) + 1
        self._polls[handle.id] = count
        if count < self.polls_before_done:
            return RunStatus(state=RunState.RUNNING)
        state = RunState.DONE if self.grade is not Grade.FAILED else RunState.FAILED
        return RunStatus(
            state=state,
            verdict=Verdict(grade=self.grade, explanation="recorded by MemoryProvider"),
            usage={"polls": count},
        )

    def list_artifacts(self, handle: RunHandle) -> list[Artifact]:
        return [
            Artifact(id=f"artifact-{i}", path=path)
            for i, path in enumerate(sorted(self.files), start=1)
        ]

    def fetch_artifact(self, handle: RunHandle, artifact: Artifact) -> bytes:
        return self.files[artifact.path]

    def close_run(self, handle: RunHandle) -> None:
        self.closed.append(handle.id)

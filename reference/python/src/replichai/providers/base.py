"""The provider contract.

A provider is any backend that can run a long-lived, tool-using worker inside a
sandbox and grade its output against a rubric. The orchestrator talks only to
this interface, so swapping backends means writing one new adapter and changing
one setting.

Implementations live beside this file, one module per backend.
"""

from __future__ import annotations

from abc import ABC, abstractmethod

from replichai.model import (
    Artifact,
    RunHandle,
    RunSpec,
    RunStatus,
    WorkerRef,
    WorkerSpec,
    WorkspaceRef,
    WorkspaceSpec,
)


class ProviderError(RuntimeError):
    """A provider call failed in a way the orchestrator cannot recover from."""


class Provider(ABC):
    """Backend capable of running graded, sandboxed, tool-using work."""

    name: str = "provider"

    @property
    def output_dir(self) -> str:
        """Where inside the sandbox a worker should write its deliverable.

        The orchestrator substitutes this into the instructions, so the method
        text never hard-codes one backend's filesystem layout.
        """
        return "/workspace/outputs/"

    @abstractmethod
    def resolve_model(self, preference: str) -> str:
        """Return a concrete model identifier for a preference such as ``best``.

        Preferences are advisory. An adapter that has no notion of model tiers
        may return ``preference`` unchanged.
        """

    @abstractmethod
    def ensure_workspace(self, spec: WorkspaceSpec) -> WorkspaceRef:
        """Create (or reuse) the sandbox described by ``spec``."""

    @abstractmethod
    def ensure_worker(self, spec: WorkerSpec) -> WorkerRef:
        """Create (or reuse) the worker described by ``spec``."""

    @abstractmethod
    def start_run(
        self, worker: WorkerRef, workspace: WorkspaceRef, spec: RunSpec
    ) -> RunHandle:
        """Begin one run and return immediately."""

    @abstractmethod
    def poll(self, handle: RunHandle) -> RunStatus:
        """Report the current state of a run without blocking."""

    @abstractmethod
    def list_artifacts(self, handle: RunHandle) -> list[Artifact]:
        """List the files a run produced. Call only once the run is terminal."""

    @abstractmethod
    def fetch_artifact(self, handle: RunHandle, artifact: Artifact) -> bytes:
        """Download one artifact's bytes."""

    @abstractmethod
    def close_run(self, handle: RunHandle) -> None:
        """Release a finished run. Must be safe to call more than once."""

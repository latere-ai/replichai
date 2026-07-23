"""Driving one audit from a paper reference to a graded set of artifacts.

The orchestrator knows the shape of the work, never the backend. Everything it
touches goes through :class:`~replichai.providers.base.Provider`.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Iterator

from replichai.method import Method
from replichai.model import (
    Artifact,
    Grade,
    RunHandle,
    RunSpec,
    RunState,
    RunStatus,
    Verdict,
    WorkerSpec,
    WorkspaceSpec,
)
from replichai.providers.base import Provider

DEFAULT_WORKER_NAME = "repro-auditor"
DEFAULT_MODEL_PREFERENCE = "best"
DEFAULT_PACKAGES = {"pip": ["pypdf", "requests"]}


class AuditError(RuntimeError):
    """An audit could not be completed."""


@dataclass
class AuditResult:
    """Everything one audit produced."""

    paper: str
    handle: RunHandle
    status: RunStatus
    artifacts: list[Artifact] = field(default_factory=list)
    artifact_dir: Path | None = None

    @property
    def verdict(self) -> Verdict:
        return self.status.verdict or Verdict(Grade.PENDING)

    @property
    def passed(self) -> bool:
        return self.verdict.passed


@dataclass
class Orchestrator:
    """Runs audits against a provider."""

    provider: Provider
    method: Method
    worker_name: str = DEFAULT_WORKER_NAME
    model_preference: str = DEFAULT_MODEL_PREFERENCE
    packages: dict[str, list[str]] = field(default_factory=lambda: dict(DEFAULT_PACKAGES))
    max_revisions: int = 3

    def start(self, paper: str) -> RunHandle:
        """Begin an audit of ``paper`` and return without waiting."""
        model = self.provider.resolve_model(self.model_preference)
        workspace = self.provider.ensure_workspace(
            WorkspaceSpec(name=f"{self.worker_name}-workspace", packages=dict(self.packages))
        )
        worker = self.provider.ensure_worker(
            WorkerSpec(
                name=self.worker_name,
                instructions=self.method.instructions_for(self.provider.output_dir),
                model=model,
                description="Audits the reproducibility of a research paper.",
            )
        )
        spec = RunSpec(
            task=self.method.task_for(paper),
            rubric=self.method.rubric,
            max_revisions=self.max_revisions,
        )
        return self.provider.start_run(worker, workspace, spec)

    def watch(
        self,
        handle: RunHandle,
        *,
        interval: float = 20.0,
        timeout: float = 3600.0,
        sleep: Callable[[float], None] = time.sleep,
        clock: Callable[[], float] = time.monotonic,
    ) -> Iterator[RunStatus]:
        """Yield the run's status until it reaches a terminal state.

        Yields at least once. Raises :class:`AuditError` if ``timeout`` elapses
        first, so a wedged run surfaces instead of polling forever.
        """
        deadline = clock() + timeout
        while True:
            status = self.provider.poll(handle)
            yield status
            if status.state.terminal:
                return
            if clock() >= deadline:
                raise AuditError(
                    f"run {handle.id} did not finish within {timeout:.0f}s "
                    f"(last state: {status.state.value})"
                )
            sleep(interval)

    def collect(self, handle: RunHandle, destination: Path | str) -> list[Artifact]:
        """Download a finished run's artifacts into ``destination``.

        Compiled residue is skipped: it is machine-specific, and a reproduction
        repository should contain sources only.
        """
        target = Path(destination)
        target.mkdir(parents=True, exist_ok=True)
        collected: list[Artifact] = []
        for artifact in self.provider.list_artifacts(handle):
            if artifact.is_compiled:
                continue
            path = _safe_join(target, artifact.path)
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(self.provider.fetch_artifact(handle, artifact))
            collected.append(artifact)
        return collected

    def audit(
        self,
        paper: str,
        destination: Path | str,
        *,
        interval: float = 20.0,
        timeout: float = 3600.0,
        sleep: Callable[[float], None] = time.sleep,
        on_status: Callable[[RunStatus], None] | None = None,
    ) -> AuditResult:
        """Run one audit end to end: start, wait, collect, release."""
        handle = self.start(paper)
        status = RunStatus(state=RunState.PENDING)
        try:
            for status in self.watch(
                handle, interval=interval, timeout=timeout, sleep=sleep
            ):
                if on_status is not None:
                    on_status(status)
            artifacts = self.collect(handle, destination)
        finally:
            self.provider.close_run(handle)
        return AuditResult(
            paper=paper,
            handle=handle,
            status=status,
            artifacts=artifacts,
            artifact_dir=Path(destination),
        )


def _safe_join(root: Path, relative: str) -> Path:
    """Resolve ``relative`` under ``root``, refusing to escape it.

    Artifact paths come from a remote sandbox, so they are untrusted input;
    an absolute path or one containing ``..`` must not write outside ``root``.
    """
    candidate = (root / relative).resolve()
    root_resolved = root.resolve()
    if candidate == root_resolved or root_resolved not in candidate.parents:
        raise AuditError(f"artifact path escapes the destination directory: {relative!r}")
    return candidate

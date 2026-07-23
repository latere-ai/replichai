"""Domain types shared by the orchestrator and every provider.

These types are deliberately free of any provider's vocabulary. A provider
adapter translates between these and whatever its backend calls things.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum


class RunState(str, Enum):
    """Lifecycle of a single audit run."""

    PENDING = "pending"
    RUNNING = "running"
    DONE = "done"
    FAILED = "failed"

    @property
    def terminal(self) -> bool:
        return self in (RunState.DONE, RunState.FAILED)


class Grade(str, Enum):
    """Outcome of grading a finished run against the rubric."""

    PASSED = "passed"
    NEEDS_REVISION = "needs_revision"
    EXHAUSTED = "exhausted"
    FAILED = "failed"
    PENDING = "pending"


@dataclass(frozen=True)
class WorkspaceSpec:
    """The sandbox a worker runs inside."""

    name: str
    packages: dict[str, list[str]] = field(default_factory=dict)
    network: str = "open"


@dataclass(frozen=True)
class WorkerSpec:
    """The worker itself: who it is and how it behaves."""

    name: str
    instructions: str
    model: str
    description: str = ""


@dataclass(frozen=True)
class RunSpec:
    """One unit of work: a task, the bar it is graded against, and a retry budget."""

    task: str
    rubric: str
    max_revisions: int = 3


@dataclass(frozen=True)
class WorkspaceRef:
    id: str


@dataclass(frozen=True)
class WorkerRef:
    id: str
    revision: int = 1


@dataclass(frozen=True)
class RunHandle:
    """Provider-opaque pointer to a run in flight."""

    id: str
    worker: WorkerRef | None = None
    workspace: WorkspaceRef | None = None


@dataclass(frozen=True)
class Verdict:
    grade: Grade
    explanation: str = ""

    @property
    def passed(self) -> bool:
        return self.grade is Grade.PASSED


@dataclass(frozen=True)
class RunStatus:
    state: RunState
    verdict: Verdict | None = None
    usage: dict[str, object] = field(default_factory=dict)


@dataclass(frozen=True)
class Artifact:
    """One file produced by a run."""

    id: str
    path: str

    @property
    def is_compiled(self) -> bool:
        """Build residue that should never reach a reproduction repository."""
        return self.path.endswith((".pyc", ".pyo")) or "__pycache__/" in self.path

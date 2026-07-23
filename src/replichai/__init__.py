"""replichai — audits the reproducibility of research papers.

Given a paper reference, it separates the parts a machine can re-run from the
parts that need people, implements and runs the former, records the decisions
the paper never wrote down, and grades the result against a fixed rubric.
"""

from replichai.method import Method, load_method
from replichai.model import Artifact, Grade, RunState, RunStatus, Verdict
from replichai.orchestrator import AuditError, AuditResult, Orchestrator
from replichai.providers import get_provider

__version__ = "0.1.0"

__all__ = [
    "Artifact",
    "AuditError",
    "AuditResult",
    "Grade",
    "Method",
    "Orchestrator",
    "RunState",
    "RunStatus",
    "Verdict",
    "get_provider",
    "load_method",
]

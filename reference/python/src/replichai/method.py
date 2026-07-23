"""Loading the audit method from disk.

The method is data, not code: the worker's standing instructions, the rubric it
is graded against, and the task template a paper reference is substituted into.
Editing the rubric costs nothing at runtime, so it lives in a file rather than
in the worker definition.
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

PAPER_PLACEHOLDER = "{{PAPER}}"
OUTPUT_DIR_PLACEHOLDER = "{{OUTPUT_DIR}}"

DEFAULT_METHOD_DIR = Path(__file__).resolve().parents[4] / "method"


class MethodError(RuntimeError):
    """The method directory is missing a required file or is malformed."""


@dataclass(frozen=True)
class Method:
    """The three documents that define an audit."""

    instructions: str
    rubric: str
    task_template: str

    def task_for(self, paper: str) -> str:
        """Render the task for one paper reference."""
        paper = paper.strip()
        if not paper:
            raise MethodError("paper reference is empty")
        if PAPER_PLACEHOLDER not in self.task_template:
            raise MethodError(
                f"task template does not contain {PAPER_PLACEHOLDER!r}"
            )
        return self.task_template.replace(PAPER_PLACEHOLDER, paper)

    def instructions_for(self, output_dir: str) -> str:
        """Render the instructions against one backend's output location."""
        return self.instructions.replace(OUTPUT_DIR_PLACEHOLDER, output_dir)


def load_method(directory: Path | str | None = None) -> Method:
    """Read the method from ``directory`` (defaults to the repo's ``method/``)."""
    base = Path(directory) if directory is not None else DEFAULT_METHOD_DIR
    if not base.is_dir():
        raise MethodError(f"method directory not found: {base}")

    def read(name: str) -> str:
        path = base / name
        if not path.is_file():
            raise MethodError(f"method directory is missing {name}: {base}")
        text = path.read_text(encoding="utf-8").strip()
        if not text:
            raise MethodError(f"method file is empty: {path}")
        return text

    return Method(
        instructions=read("instructions.md"),
        rubric=read("rubric.md"),
        task_template=read("task-template.md"),
    )

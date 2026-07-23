from pathlib import Path

import pytest

from replichai.method import Method, MethodError, load_method


def write_method(root: Path, **overrides: str) -> Path:
    files = {
        "instructions.md": "You audit papers.",
        "rubric.md": "1. Verdict.",
        "task-template.md": "Audit {{PAPER}} and report.",
    }
    files.update(overrides)
    root.mkdir(parents=True, exist_ok=True)
    for name, text in files.items():
        (root / name).write_text(text, encoding="utf-8")
    return root


def test_load_method_reads_all_three_documents(tmp_path):
    method = load_method(write_method(tmp_path / "method"))
    assert method.instructions == "You audit papers."
    assert method.rubric == "1. Verdict."
    assert "{{PAPER}}" in method.task_template


def test_task_for_substitutes_the_paper(tmp_path):
    method = load_method(write_method(tmp_path / "method"))
    assert method.task_for("https://arxiv.org/abs/1") == (
        "Audit https://arxiv.org/abs/1 and report."
    )


def test_task_for_rejects_an_empty_reference(tmp_path):
    method = load_method(write_method(tmp_path / "method"))
    with pytest.raises(MethodError, match="empty"):
        method.task_for("   ")


def test_task_for_requires_the_placeholder():
    method = Method(instructions="i", rubric="r", task_template="no placeholder here")
    with pytest.raises(MethodError, match=r"does not contain"):
        method.task_for("paper")


def test_missing_directory_is_an_error(tmp_path):
    with pytest.raises(MethodError, match="not found"):
        load_method(tmp_path / "absent")


def test_missing_file_is_an_error(tmp_path):
    root = write_method(tmp_path / "method")
    (root / "rubric.md").unlink()
    with pytest.raises(MethodError, match="missing rubric.md"):
        load_method(root)


def test_empty_file_is_an_error(tmp_path):
    root = write_method(tmp_path / "method", **{"rubric.md": "   \n"})
    with pytest.raises(MethodError, match="empty"):
        load_method(root)


def test_the_shipped_method_loads():
    """The repository's own method/ directory must always be valid."""
    method = load_method()
    assert "inner" in method.instructions.lower()
    assert method.task_for("X").count("X") >= 1

import pytest

from replichai.cli import _slugify, main
from replichai.orchestrator import AuditError


def test_providers_command_lists_providers(capsys):
    assert main(["providers"]) == 0
    out = capsys.readouterr().out
    assert "memory" in out and "hosted" in out


def test_audit_reports_the_verdict(tmp_path, capsys, monkeypatch):
    monkeypatch.chdir(tmp_path)
    method = tmp_path / "method"
    method.mkdir()
    (method / "instructions.md").write_text("audit")
    (method / "rubric.md").write_text("rubric")
    (method / "task-template.md").write_text("do {{PAPER}}")

    code = main(
        [
            "audit",
            "https://arxiv.org/abs/1",
            "--provider",
            "memory",
            "--method",
            str(method),
            "--out",
            str(tmp_path / "runs"),
            "--interval",
            "0",
        ]
    )

    assert code == 0
    assert "passed" in capsys.readouterr().out


def test_audit_reports_a_bad_method_directory(capsys):
    assert main(["audit", "paper", "--method", "/nonexistent", "--provider", "memory"]) == 2
    assert "error" in capsys.readouterr().err


def test_audit_reports_an_unknown_provider(capsys):
    assert main(["audit", "paper", "--provider", "nope"]) == 2
    assert "unknown provider" in capsys.readouterr().err


def test_audit_surfaces_a_failed_run_as_a_nonzero_exit(tmp_path, capsys, monkeypatch):
    """A run that errors must report on stderr and exit non-zero, not traceback."""
    method = tmp_path / "method"
    method.mkdir()
    (method / "instructions.md").write_text("audit")
    (method / "rubric.md").write_text("rubric")
    (method / "task-template.md").write_text("do {{PAPER}}")

    def explode(*args, **kwargs):
        raise AuditError("run did not finish within 0s")

    monkeypatch.setattr("replichai.cli.Orchestrator.audit", explode)

    code = main(
        [
            "audit",
            "paper",
            "--provider",
            "memory",
            "--method",
            str(method),
            "--out",
            str(tmp_path / "runs"),
        ]
    )

    assert code == 1
    err = capsys.readouterr().err
    assert "error" in err and "did not finish" in err


@pytest.mark.parametrize(
    ("reference", "expected"),
    [
        ("https://arxiv.org/abs/2603.01795", "https-arxiv-org-abs-2603-01795"),
        ("  Mixed CASE  ", "mixed-case"),
        ("///", "paper"),
    ],
)
def test_slugify(reference, expected):
    assert _slugify(reference) == expected


def test_slugify_is_bounded():
    assert len(_slugify("x" * 500)) <= 80

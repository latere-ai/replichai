import pytest

from replichai.method import Method
from replichai.model import Artifact, Grade, RunHandle, RunState, RunStatus
from replichai.orchestrator import AuditError, Orchestrator
from replichai.providers.memory import MemoryProvider

METHOD = Method(
    instructions="Audit papers. Never fabricate. Write to {{OUTPUT_DIR}}.",
    rubric="1. Verdict\n2. Boundary",
    task_template="Audit {{PAPER}}.",
)


def make(provider: MemoryProvider) -> Orchestrator:
    return Orchestrator(provider=provider, method=METHOD)


def test_start_passes_the_method_through_to_the_provider():
    provider = MemoryProvider()
    make(provider).start("https://arxiv.org/abs/2603.01795")

    assert provider.started[0].rubric == METHOD.rubric
    assert "2603.01795" in provider.started[0].task
    assert provider.started[0].max_revisions == 3


def test_start_resolves_the_output_directory_from_the_provider():
    """The method never hard-codes one backend's filesystem layout."""
    provider = MemoryProvider()
    make(provider).start("paper")

    instructions = provider.workers[0].instructions
    assert "{{OUTPUT_DIR}}" not in instructions
    assert provider.output_dir in instructions


def test_start_resolves_the_model_and_packages():
    provider = MemoryProvider(model="some-model")
    make(provider).start("paper")

    assert provider.workers[0].model == "some-model"
    assert provider.workspaces[0].packages == {"pip": ["pypdf", "requests"]}


def test_watch_yields_until_terminal():
    provider = MemoryProvider(polls_before_done=3)
    orchestrator = make(provider)
    handle = orchestrator.start("paper")

    states = [s.state for s in orchestrator.watch(handle, interval=0, sleep=lambda _: None)]

    assert states == [RunState.RUNNING, RunState.RUNNING, RunState.DONE]


def test_watch_raises_when_the_run_outlives_the_timeout():
    provider = MemoryProvider(polls_before_done=99)
    orchestrator = make(provider)
    handle = orchestrator.start("paper")
    ticks = iter([0.0, 10.0, 20.0, 30.0, 40.0])

    with pytest.raises(AuditError, match="did not finish"):
        list(
            orchestrator.watch(
                handle,
                interval=0,
                timeout=15.0,
                sleep=lambda _: None,
                clock=lambda: next(ticks),
            )
        )


def test_collect_writes_artifacts_to_disk(tmp_path):
    provider = MemoryProvider(
        files={
            "README.md": b"# repo",
            "src/core.py": b"print(1)",
        }
    )
    orchestrator = make(provider)
    handle = orchestrator.start("paper")

    collected = orchestrator.collect(handle, tmp_path / "out")

    assert {a.path for a in collected} == {"README.md", "src/core.py"}
    assert (tmp_path / "out" / "README.md").read_bytes() == b"# repo"
    assert (tmp_path / "out" / "src" / "core.py").read_bytes() == b"print(1)"


def test_collect_skips_compiled_residue(tmp_path):
    provider = MemoryProvider(
        files={
            "core.py": b"source",
            "__pycache__/core.cpython-311.pyc": b"\x00compiled",
            "core.pyo": b"\x00compiled",
        }
    )
    orchestrator = make(provider)
    handle = orchestrator.start("paper")

    collected = orchestrator.collect(handle, tmp_path / "out")

    assert [a.path for a in collected] == ["core.py"]
    assert not (tmp_path / "out" / "__pycache__").exists()
    assert not (tmp_path / "out" / "core.pyo").exists()


@pytest.mark.parametrize(
    "hostile",
    ["../escaped.md", "nested/../../escaped.md", "/etc/passwd"],
)
def test_collect_refuses_paths_that_escape_the_destination(tmp_path, hostile):
    """Artifact paths come from a remote sandbox and are untrusted input."""
    provider = MemoryProvider(files={hostile: b"payload"})
    orchestrator = make(provider)
    handle = orchestrator.start("paper")

    with pytest.raises(AuditError, match="escapes"):
        orchestrator.collect(handle, tmp_path / "out")

    assert not (tmp_path / "escaped.md").exists()


def test_audit_runs_end_to_end_and_releases_the_run(tmp_path):
    provider = MemoryProvider(polls_before_done=2, files={"REPRODUCIBILITY.md": b"40%"})
    orchestrator = make(provider)
    seen: list[RunStatus] = []

    result = orchestrator.audit(
        "https://arxiv.org/abs/2603.01795",
        tmp_path / "out",
        interval=0,
        sleep=lambda _: None,
        on_status=seen.append,
    )

    assert result.passed
    assert result.verdict.grade is Grade.PASSED
    assert [a.path for a in result.artifacts] == ["REPRODUCIBILITY.md"]
    assert provider.closed == [result.handle.id]
    assert [s.state for s in seen] == [RunState.RUNNING, RunState.DONE]


def test_audit_releases_the_run_even_when_it_fails(tmp_path):
    provider = MemoryProvider(polls_before_done=99)
    orchestrator = make(provider)

    with pytest.raises(AuditError):
        orchestrator.audit(
            "paper", tmp_path / "out", interval=0, timeout=0, sleep=lambda _: None
        )

    assert provider.closed, "a failed run must still be released"


def test_a_failing_grade_is_not_a_pass(tmp_path):
    provider = MemoryProvider(grade=Grade.NEEDS_REVISION)
    orchestrator = make(provider)

    result = orchestrator.audit(
        "paper", tmp_path / "out", interval=0, sleep=lambda _: None
    )

    assert not result.passed


def test_result_verdict_defaults_to_pending():
    from replichai.orchestrator import AuditResult

    result = AuditResult(
        paper="p", handle=RunHandle(id="r"), status=RunStatus(state=RunState.PENDING)
    )
    assert result.verdict.grade is Grade.PENDING
    assert not result.passed


def test_artifact_recognises_compiled_files():
    assert Artifact(id="1", path="a/__pycache__/x.cpython-311.pyc").is_compiled
    assert Artifact(id="2", path="x.pyo").is_compiled
    assert not Artifact(id="3", path="x.py").is_compiled

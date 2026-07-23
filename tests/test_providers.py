import json
import os
from unittest import mock

import pytest

from replichai.model import (
    Grade,
    RunHandle,
    RunSpec,
    RunState,
    WorkerRef,
    WorkerSpec,
    WorkspaceRef,
    WorkspaceSpec,
)
from replichai.providers import DEFAULT_PROVIDER, ProviderError, available, get_provider
from replichai.providers.base import Provider
from replichai.providers.hosted import HostedProvider
from replichai.providers.memory import MemoryProvider


class TestRegistry:
    def test_lists_the_known_providers(self):
        assert set(available()) == {"hosted", "memory"}

    def test_instantiates_by_name(self):
        assert isinstance(get_provider("memory"), MemoryProvider)

    def test_rejects_an_unknown_name(self):
        with pytest.raises(ProviderError, match="unknown provider"):
            get_provider("nope")

    def test_default_is_registered(self):
        assert DEFAULT_PROVIDER in available()

    def test_every_registered_provider_satisfies_the_contract(self):
        """Whatever the registry exposes must be a complete Provider."""
        abstract = {
            name
            for name in dir(Provider)
            if getattr(getattr(Provider, name), "__isabstractmethod__", False)
        }
        with mock.patch.dict(os.environ, {"REPLICHAI_API_KEY": "test-credential"}):
            for name in available():
                provider = get_provider(name)
                assert isinstance(provider, Provider)
                assert not getattr(provider, "__abstractmethods__", set())
                for method in abstract:
                    assert callable(getattr(provider, method))


class TestHostedCredentials:
    def test_refuses_to_start_without_a_credential(self):
        with mock.patch.dict(os.environ, {"REPLICHAI_API_KEY": ""}):
            with pytest.raises(ProviderError, match="no credential"):
                HostedProvider()

    def test_reads_the_credential_from_the_environment(self):
        with mock.patch.dict(os.environ, {"REPLICHAI_API_KEY": "from-env"}):
            assert HostedProvider().api_key == "from-env"

    def test_the_credential_is_never_placed_in_a_url(self):
        provider = HostedProvider(api_key="secret-value")
        captured: list[str] = []

        def fake_urlopen(request, timeout=None):
            captured.append(request.full_url)
            raise AssertionError("stop here")

        with mock.patch("urllib.request.urlopen", fake_urlopen):
            with pytest.raises(AssertionError):
                provider._request("GET", "/models")

        assert captured and "secret-value" not in captured[0]


class _FakeResponse:
    def __init__(self, payload):
        self._body = payload if isinstance(payload, bytes) else json.dumps(payload).encode()

    def read(self):
        return self._body

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class TestHostedTranslation:
    """The adapter must map backend vocabulary onto the neutral contract."""

    def provider(self):
        return HostedProvider(api_key="test-credential")

    def test_resolve_model_prefers_the_requested_tier(self):
        provider = self.provider()
        payload = {"data": [{"id": "fast-model-1"}, {"id": "opus-model-2"}]}
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse(payload)):
            assert provider.resolve_model("best") == "opus-model-2"

    def test_resolve_model_falls_back_to_the_first_model(self):
        provider = self.provider()
        payload = {"data": [{"id": "only-model"}]}
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse(payload)):
            assert provider.resolve_model("best") == "only-model"

    def test_resolve_model_errors_when_nothing_is_offered(self):
        provider = self.provider()
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse({"data": []})):
            with pytest.raises(ProviderError, match="no models"):
                provider.resolve_model("best")

    @pytest.mark.parametrize(
        ("backend_status", "backend_grade", "state", "grade"),
        [
            ("running", None, RunState.RUNNING, None),
            ("idle", "satisfied", RunState.DONE, Grade.PASSED),
            ("idle", "needs_revision", RunState.DONE, Grade.NEEDS_REVISION),
            ("idle", "max_iterations_reached", RunState.DONE, Grade.EXHAUSTED),
            ("terminated", "failed", RunState.DONE, Grade.FAILED),
        ],
    )
    def test_poll_translates_states_and_grades(
        self, backend_status, backend_grade, state, grade
    ):
        provider = self.provider()
        payload = {"status": backend_status, "usage": {"tokens": 1}}
        if backend_grade is not None:
            payload["outcome_evaluations"] = [
                {"result": backend_grade, "explanation": "because"}
            ]
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse(payload)):
            status = provider.poll(RunHandle(id="run-1"))

        assert status.state is state
        if grade is None:
            assert status.verdict is None
        else:
            assert status.verdict.grade is grade
            assert status.verdict.explanation == "because"

    def test_a_finished_run_without_a_grade_is_a_failure(self):
        """Silence from the grader must never read as success."""
        provider = self.provider()
        with mock.patch(
            "urllib.request.urlopen", return_value=_FakeResponse({"status": "idle"})
        ):
            status = provider.poll(RunHandle(id="run-1"))

        assert status.state is RunState.FAILED
        assert not (status.verdict and status.verdict.passed)

    def test_start_run_sends_the_task_and_rubric(self):
        provider = self.provider()
        sent: list[dict] = []

        def fake_urlopen(request, timeout=None):
            body = json.loads(request.data.decode()) if request.data else {}
            sent.append(body)
            return _FakeResponse({"id": "run-9"})

        with mock.patch("urllib.request.urlopen", fake_urlopen):
            handle = provider.start_run(
                WorkerRef(id="w"),
                WorkspaceRef(id="e"),
                RunSpec(task="do it", rubric="check it", max_revisions=5),
            )

        assert handle.id == "run-9"
        kickoff = sent[-1]["events"][0]
        assert kickoff["description"] == "do it"
        assert kickoff["rubric"]["content"] == "check it"
        assert kickoff["max_iterations"] == 5

    def test_ensure_worker_returns_the_revision(self):
        provider = self.provider()
        payload = {"id": "worker-1", "version": 4}
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse(payload)):
            worker = provider.ensure_worker(
                WorkerSpec(name="n", instructions="i", model="m")
            )
        assert worker == WorkerRef(id="worker-1", revision=4)

    def test_ensure_workspace_passes_packages_through(self):
        provider = self.provider()
        sent: list[dict] = []

        def fake_urlopen(request, timeout=None):
            sent.append(json.loads(request.data.decode()))
            return _FakeResponse({"id": "workspace-1"})

        with mock.patch("urllib.request.urlopen", fake_urlopen):
            ref = provider.ensure_workspace(
                WorkspaceSpec(name="w", packages={"pip": ["pypdf"]})
            )

        assert ref.id == "workspace-1"
        assert sent[0]["config"]["packages"] == {"pip": ["pypdf"]}

    def test_list_artifacts_maps_filenames_to_paths(self):
        provider = self.provider()
        payload = {"data": [{"id": "f1", "filename": "src/a.py"}]}
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse(payload)):
            artifacts = provider.list_artifacts(RunHandle(id="run-1"))
        assert artifacts[0].id == "f1"
        assert artifacts[0].path == "src/a.py"

    def test_fetch_artifact_returns_raw_bytes(self):
        from replichai.model import Artifact

        provider = self.provider()
        with mock.patch(
            "urllib.request.urlopen", return_value=_FakeResponse(b"\x89PNG raw")
        ):
            data = provider.fetch_artifact(
                RunHandle(id="r"), Artifact(id="f1", path="x.png")
            )
        assert data == b"\x89PNG raw"

    def test_responses_with_control_characters_still_parse(self):
        """Payloads echo the worker's instructions verbatim, newlines and all."""
        provider = self.provider()
        raw = b'{"id": "run-1", "system": "line one\nline two"}'
        with mock.patch("urllib.request.urlopen", return_value=_FakeResponse(raw)):
            assert provider._request("GET", "/sessions/run-1")["id"] == "run-1"

    def test_close_run_tolerates_an_already_closed_run(self):
        provider = self.provider()
        with mock.patch.object(
            provider, "_request", side_effect=ProviderError("already archived")
        ):
            provider.close_run(RunHandle(id="run-1"))  # must not raise


class TestMemoryProvider:
    def test_records_what_it_was_asked_to_do(self):
        provider = MemoryProvider()
        workspace = provider.ensure_workspace(WorkspaceSpec(name="w"))
        worker = provider.ensure_worker(
            WorkerSpec(name="n", instructions="i", model="m")
        )
        handle = provider.start_run(worker, workspace, RunSpec(task="t", rubric="r"))
        provider.close_run(handle)

        assert provider.workspaces and provider.workers and provider.started
        assert provider.closed == [handle.id]

    def test_each_run_gets_its_own_identity(self):
        provider = MemoryProvider()
        workspace = provider.ensure_workspace(WorkspaceSpec(name="w"))
        worker = provider.ensure_worker(WorkerSpec(name="n", instructions="i", model="m"))
        spec = RunSpec(task="t", rubric="r")

        first = provider.start_run(worker, workspace, spec)
        second = provider.start_run(worker, workspace, spec)

        assert first.id != second.id

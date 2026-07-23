"""Adapter for the hosted agent backend currently in use.

This is the only module in the package that knows the backend's vocabulary,
endpoints, or payload shapes. Everything above it speaks the neutral contract in
``providers/base.py``; porting to another backend means writing a sibling of
this file and changing one setting.

Configuration comes from the environment:

``REPLICHAI_API_KEY``  credential (required)
``REPLICHAI_API_BASE`` API root (optional; has a default)
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any

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
from replichai.providers.base import Provider, ProviderError

API_BASE = "https://api.anthropic.com/v1"
API_VERSION = "2023-06-01"
API_FEATURE = "managed-agents-2026-04-01"
TOOLSET = "agent_toolset_20260401"
MODEL_TIERS = {"best": "opus", "fast": "haiku", "balanced": "sonnet"}

# Backend grade names -> neutral grades.
_GRADES = {
    "satisfied": Grade.PASSED,
    "needs_revision": Grade.NEEDS_REVISION,
    "max_iterations_reached": Grade.EXHAUSTED,
    "failed": Grade.FAILED,
    "interrupted": Grade.FAILED,
    "pending": Grade.PENDING,
}

# Backend run states -> neutral states.
_STATES = {
    "idle": RunState.DONE,
    "running": RunState.RUNNING,
    "rescheduling": RunState.RUNNING,
    "terminated": RunState.DONE,
}


@dataclass
class HostedProvider(Provider):
    """Talks to the hosted backend over HTTP."""

    name: str = "hosted"
    api_key: str = field(default_factory=lambda: os.environ.get("REPLICHAI_API_KEY", ""))
    api_base: str = field(
        default_factory=lambda: os.environ.get("REPLICHAI_API_BASE", API_BASE)
    )
    timeout: float = 60.0

    @property
    def output_dir(self) -> str:
        return "/mnt/session/outputs/"

    def __post_init__(self) -> None:
        if not self.api_key:
            raise ProviderError(
                "no credential: set REPLICHAI_API_KEY in the environment or pass api_key"
            )

    # ---------------------------------------------------------------- transport

    def _request(
        self, method: str, path: str, payload: dict[str, Any] | None = None
    ) -> Any:
        url = f"{self.api_base}{path}"
        body = json.dumps(payload).encode() if payload is not None else None
        request = urllib.request.Request(url, data=body, method=method)
        request.add_header("x-api-key", self.api_key)
        request.add_header("anthropic-version", API_VERSION)
        request.add_header("anthropic-beta", API_FEATURE)
        request.add_header("content-type", "application/json")
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                raw = response.read()
        except urllib.error.HTTPError as exc:  # pragma: no cover - network path
            detail = exc.read().decode(errors="replace")[:500]
            raise ProviderError(f"{method} {path} failed ({exc.code}): {detail}") from exc
        except urllib.error.URLError as exc:  # pragma: no cover - network path
            raise ProviderError(f"{method} {path} failed: {exc.reason}") from exc
        if not raw:
            return None
        # Payloads embed the worker's instructions verbatim, control characters
        # and all, so the strict JSON grammar rejects them.
        return json.JSONDecoder(strict=False).decode(raw.decode())

    def _download(self, path: str) -> bytes:
        url = f"{self.api_base}{path}"
        request = urllib.request.Request(url, method="GET")
        request.add_header("x-api-key", self.api_key)
        request.add_header("anthropic-version", API_VERSION)
        request.add_header("anthropic-beta", API_FEATURE)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                return response.read()
        except urllib.error.URLError as exc:  # pragma: no cover - network path
            raise ProviderError(f"download {path} failed: {exc}") from exc

    # ---------------------------------------------------------------- contract

    def resolve_model(self, preference: str) -> str:
        tier = MODEL_TIERS.get(preference, preference)
        data = self._request("GET", "/models") or {}
        ids = [entry["id"] for entry in data.get("data", [])]
        if not ids:
            raise ProviderError("backend returned no models")
        matches = [i for i in ids if tier in i]
        return matches[0] if matches else ids[0]

    def ensure_workspace(self, spec: WorkspaceSpec) -> WorkspaceRef:
        network = (
            {"type": "unrestricted"}
            if spec.network == "open"
            else {"type": "limited", "allowed_hosts": []}
        )
        payload = {
            "name": spec.name,
            "config": {
                "type": "cloud",
                "networking": network,
                "packages": dict(spec.packages),
            },
        }
        data = self._request("POST", "/environments", payload) or {}
        return WorkspaceRef(id=data["id"])

    def ensure_worker(self, spec: WorkerSpec) -> WorkerRef:
        payload = {
            "name": spec.name,
            "description": spec.description,
            "model": spec.model,
            "system": spec.instructions,
            "tools": [{"type": TOOLSET}],
        }
        data = self._request("POST", "/agents", payload) or {}
        return WorkerRef(id=data["id"], revision=int(data.get("version", 1)))

    def start_run(
        self, worker: WorkerRef, workspace: WorkspaceRef, spec: RunSpec
    ) -> RunHandle:
        session = (
            self._request(
                "POST",
                "/sessions",
                {
                    "agent": worker.id,
                    "environment_id": workspace.id,
                    "title": "reproducibility audit",
                },
            )
            or {}
        )
        run_id = session["id"]
        self._request(
            "POST",
            f"/sessions/{run_id}/events",
            {
                "events": [
                    {
                        "type": "user.define_outcome",
                        "description": spec.task,
                        "rubric": {"type": "text", "content": spec.rubric},
                        "max_iterations": spec.max_revisions,
                    }
                ]
            },
        )
        return RunHandle(id=run_id, worker=worker, workspace=workspace)

    def poll(self, handle: RunHandle) -> RunStatus:
        data = self._request("GET", f"/sessions/{handle.id}") or {}
        state = _STATES.get(data.get("status", ""), RunState.RUNNING)
        evaluations = data.get("outcome_evaluations") or []
        verdict = None
        if evaluations:
            last = evaluations[-1]
            verdict = Verdict(
                grade=_GRADES.get(last.get("result", ""), Grade.PENDING),
                explanation=last.get("explanation", "") or "",
            )
        # A finished run whose grader never returned is a failure, not a pass.
        if state is RunState.DONE and verdict is None:
            state = RunState.FAILED
        return RunStatus(state=state, verdict=verdict, usage=data.get("usage") or {})

    def list_artifacts(self, handle: RunHandle) -> list[Artifact]:
        data = self._request("GET", f"/files?scope_id={handle.id}") or {}
        return [
            Artifact(id=entry["id"], path=entry["filename"])
            for entry in data.get("data", [])
        ]

    def fetch_artifact(self, handle: RunHandle, artifact: Artifact) -> bytes:
        return self._download(f"/files/{artifact.id}/content")

    def close_run(self, handle: RunHandle) -> None:
        try:
            self._request("POST", f"/sessions/{handle.id}/archive", {})
        except ProviderError:
            # Releasing a run is best-effort; an already-archived run is fine.
            pass

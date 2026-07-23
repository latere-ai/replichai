"""Command line entry point.

    replichai audit <paper-url> [--out DIR]
    replichai providers
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Sequence

from replichai.method import MethodError, load_method
from replichai.model import RunStatus
from replichai.orchestrator import AuditError, Orchestrator
from replichai.providers import DEFAULT_PROVIDER, ProviderError, available, get_provider


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="replichai", description="Audit the reproducibility of a research paper."
    )
    sub = parser.add_subparsers(dest="command", required=True)

    audit = sub.add_parser("audit", help="audit one paper")
    audit.add_argument("paper", help="arXiv / DOI / publisher URL")
    audit.add_argument(
        "--out", default="runs", help="directory for artifacts (default: runs)"
    )
    audit.add_argument("--provider", default=DEFAULT_PROVIDER)
    audit.add_argument("--method", default=None, help="path to a method directory")
    audit.add_argument("--interval", type=float, default=20.0)
    audit.add_argument("--timeout", type=float, default=3600.0)
    audit.add_argument("--max-revisions", type=int, default=3)

    sub.add_parser("providers", help="list available providers")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)

    if args.command == "providers":
        for name in available():
            marker = " (default)" if name == DEFAULT_PROVIDER else ""
            print(f"{name}{marker}")
        return 0

    try:
        method = load_method(args.method)
        provider = get_provider(args.provider)
    except (MethodError, ProviderError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    orchestrator = Orchestrator(
        provider=provider, method=method, max_revisions=args.max_revisions
    )
    destination = Path(args.out) / _slugify(args.paper)

    def report(status: RunStatus) -> None:
        print(f"  {status.state.value}", file=sys.stderr)

    try:
        result = orchestrator.audit(
            args.paper,
            destination,
            interval=args.interval,
            timeout=args.timeout,
            on_status=report,
        )
    except (AuditError, ProviderError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    print(f"verdict:   {result.verdict.grade.value}")
    print(f"artifacts: {len(result.artifacts)} -> {result.artifact_dir}")
    return 0 if result.passed else 1


def _slugify(reference: str) -> str:
    """A filesystem-safe directory name for one paper reference.

    Deliberately simple: this names a local scratch directory, not a published
    repository. Repository naming is a separate concern with its own rules.
    """
    keep = [c if c.isalnum() else "-" for c in reference.strip().lower()]
    slug = "".join(keep).strip("-")
    while "--" in slug:
        slug = slug.replace("--", "-")
    return slug[-80:].strip("-") or "paper"


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())

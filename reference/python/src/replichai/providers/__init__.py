"""Provider registry.

One adapter per backend. ``get_provider`` is the single place the rest of the
package learns which one is in use.
"""

from __future__ import annotations

from replichai.providers.base import Provider, ProviderError

_REGISTRY: dict[str, str] = {
    "hosted": "replichai.providers.hosted:HostedProvider",
    "memory": "replichai.providers.memory:MemoryProvider",
}

DEFAULT_PROVIDER = "hosted"


def available() -> list[str]:
    """Names that :func:`get_provider` accepts."""
    return sorted(_REGISTRY)


def get_provider(name: str | None = None, **kwargs: object) -> Provider:
    """Instantiate a provider by name."""
    key = (name or DEFAULT_PROVIDER).strip().lower()
    target = _REGISTRY.get(key)
    if target is None:
        raise ProviderError(
            f"unknown provider {key!r}; available: {', '.join(available())}"
        )
    module_name, _, class_name = target.partition(":")
    module = __import__(module_name, fromlist=[class_name])
    return getattr(module, class_name)(**kwargs)


__all__ = ["Provider", "ProviderError", "available", "get_provider", "DEFAULT_PROVIDER"]

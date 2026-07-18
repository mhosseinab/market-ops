"""Dev observability wiring for the LLM plane (one trace system).

Two integrations, each a strict no-op unless explicitly enabled:

* **Sentry Spotlight** — dev-only, enabled ONLY when ``SENTRY_SPOTLIGHT`` is set.
  No DSN is ever configured, so nothing ships to a Sentry cloud.
* **LangSmith native tracing** — the graph and agents emit LangSmith traces
  natively. Active ONLY when ``LANGSMITH_TRACING`` and ``LANGSMITH_API_KEY`` are
  BOTH set AND not running in CI. Traces carry prompts/completions to the
  LangSmith cloud, so non-mock enablement is a gated operation — force-disabled
  in CI regardless of the env.

Config tests assert every integration is a no-op when its env vars are unset.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

from llm.config import Settings


@dataclass(frozen=True)
class ObservabilityStatus:
    """What was actually enabled (for tests and startup logging)."""

    sentry_enabled: bool
    langsmith_enabled: bool


def configure_observability(settings: Settings) -> ObservabilityStatus:
    """Initialize observability, honoring the strict enable conditions.

    Returns the resolved status. Never raises on a missing optional dependency:
    an integration that cannot initialize stays disabled (fail closed to off).
    """
    sentry_enabled = _configure_sentry(settings)
    langsmith_enabled = _configure_langsmith(settings)
    return ObservabilityStatus(sentry_enabled=sentry_enabled, langsmith_enabled=langsmith_enabled)


def _configure_sentry(settings: Settings) -> bool:
    if not settings.sentry_enabled():
        return False
    try:
        import sentry_sdk
        from sentry_sdk.spotlight import setup_spotlight  # noqa: F401
    except Exception:
        return False
    # Spotlight ONLY — never a DSN. dsn="" keeps events out of any cloud.
    sentry_sdk.init(dsn="", spotlight=settings.sentry_spotlight, traces_sample_rate=0.0)
    return True


def _configure_langsmith(settings: Settings) -> bool:
    if not settings.langsmith_enabled():
        # Force-disable native tracing so a stray env var cannot ship traces.
        os.environ["LANGSMITH_TRACING"] = "false"
        os.environ.pop("LANGCHAIN_TRACING_V2", None)
        return False
    # Enablement is a gated op; only reached when both signals set and not CI.
    os.environ["LANGSMITH_TRACING"] = "true"
    os.environ["LANGSMITH_API_KEY"] = settings.langsmith_api_key.get_secret_value()
    return True

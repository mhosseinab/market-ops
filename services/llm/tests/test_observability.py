"""Observability config tests: every integration is a no-op unless enabled."""

from __future__ import annotations

import os

from llm.config import Settings
from llm.observability import configure_observability
from pydantic import SecretStr


def test_both_integrations_noop_when_env_unset(monkeypatch) -> None:  # type: ignore[no-untyped-def]
    monkeypatch.delenv("CI", raising=False)
    settings = Settings(
        sentry_spotlight="",
        langsmith_tracing=False,
    )
    status = configure_observability(settings)
    assert status.sentry_enabled is False
    assert status.langsmith_enabled is False
    # LangSmith native tracing is force-disabled, not left ambiguous.
    assert os.environ.get("LANGSMITH_TRACING") == "false"


def test_langsmith_force_disabled_in_ci(monkeypatch) -> None:  # type: ignore[no-untyped-def]
    monkeypatch.setenv("CI", "true")
    settings = Settings(
        langsmith_tracing=True,
        langsmith_api_key=SecretStr("a-key"),  # both signals set, but CI forces off
    )
    assert settings.langsmith_enabled() is False
    status = configure_observability(settings)
    assert status.langsmith_enabled is False


def test_sentry_enabled_only_with_spotlight(monkeypatch) -> None:  # type: ignore[no-untyped-def]
    monkeypatch.delenv("CI", raising=False)
    off = Settings(sentry_spotlight="")
    assert off.sentry_enabled() is False
    on = Settings(sentry_spotlight="http://localhost:8969/stream")
    assert on.sentry_enabled() is True


def test_langsmith_needs_both_signals(monkeypatch) -> None:  # type: ignore[no-untyped-def]
    monkeypatch.delenv("CI", raising=False)
    only_flag = Settings(langsmith_tracing=True, langsmith_api_key=SecretStr(""))
    assert only_flag.langsmith_enabled() is False
    only_key = Settings(langsmith_tracing=False, langsmith_api_key=SecretStr("k"))
    assert only_key.langsmith_enabled() is False
    both = Settings(langsmith_tracing=True, langsmith_api_key=SecretStr("k"))
    assert both.langsmith_enabled() is True

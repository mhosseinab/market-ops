"""Configuration for the LLM plane (PRD §12.1, §19.3).

Every operational parameter is data, loaded from the process environment via
``pydantic-settings`` — model/provider selection is *configuration*, never a
code branch (§12.1). The provider is OpenAI-compatible ONLY; the sole choice is
between the deterministic in-process mock (used for ALL tests and CI — no paid
calls, ever) and a configured OpenAI-compatible endpoint reached through
``langchain-openai``'s ``ChatOpenAI(base_url=...)``. There is NO vendor-SDK
branch.

Hard per-turn bounds live here too (recursion limit, tool-call limits, per-tool
timeout, token ceiling); the orchestrator reads them so a run can never loop
unbounded or truncate silently (§12.4). The LLM plane has NO database
credential (§19.3), so there is no checkpointer / durable-state configuration:
conversation durability is the gateway's concern.
"""

from __future__ import annotations

import os
from enum import StrEnum
from typing import Any

from pydantic import SecretStr
from pydantic_settings import BaseSettings, SettingsConfigDict


class ProviderKind(StrEnum):
    """The two OpenAI-compatible transports. Never a vendor SDK branch."""

    MOCK = "mock"
    OPENAI_COMPATIBLE = "openai_compatible"


class Settings(BaseSettings):
    """Resolved, validated LLM-plane configuration.

    Read from ``LLM_*`` environment variables. Defaults are safe for tests and
    local dev: the deterministic mock provider, observability off, chat enabled.
    """

    model_config = SettingsConfigDict(
        env_prefix="LLM_",
        env_file=None,
        extra="ignore",
    )

    # --- provider (OpenAI-compatible only) -----------------------------------
    provider_kind: ProviderKind = ProviderKind.MOCK
    # Base URL of the OpenAI-compatible endpoint. Only used when provider_kind
    # is OPENAI_COMPATIBLE; the mock ignores it. Selecting the endpoint is
    # configuration measured at Gate 0a, not an ad-hoc switch.
    provider_base_url: str = "http://localhost:11434/v1"
    # Credential *reference*: the API key presented to the endpoint. A SecretStr
    # so it never renders in logs/reprs. Empty for the mock.
    provider_api_key: SecretStr = SecretStr("")
    provider_model: str = "mock-model"
    provider_timeout_seconds: float = 30.0
    # Qualified capabilities of the selected model (e.g. "intent", "briefing").
    # Data only; the harness records what a model was qualified for at the gate.
    provider_capabilities: tuple[str, ...] = ()
    # Per-turn token ceiling enforced via the model config (max_tokens). No
    # silent truncation: exceeding a bound maps to the §12.4 structured failure.
    max_output_tokens: int = 1024

    # --- hard per-turn bounds (§12.4) ----------------------------------------
    # Graph recursion limit per turn. A GraphRecursionError maps to the §12.4
    # structured failure state.
    graph_recursion_limit: int = 24
    # Global tool-call run limit across a turn (ToolCallLimitMiddleware).
    tool_call_run_limit: int = 12
    # Per-tool run limit within a turn.
    per_tool_call_run_limit: int = 4
    # Per-tool timeout (seconds).
    per_tool_timeout_seconds: float = 15.0
    # Single §12.4 transient retry at the node level — never stacked.
    node_transient_retries: int = 1

    # --- kill switch (CHAT-009) ----------------------------------------------
    # The authoritative kill switch is the gateway's; the LLM plane also honors a
    # local disabled state so a direct probe degrades cleanly. Chat-only: never
    # affects screens.
    chat_disabled_global: bool = False
    chat_disabled_accounts: frozenset[str] = frozenset()

    # --- observability (no-op unless explicitly enabled) ---------------------
    # Dev-only Sentry Spotlight (no DSN, ever). Empty ⇒ Sentry fully disabled.
    sentry_spotlight: str = ""
    # LangSmith native tracing. Active ONLY when BOTH the flag and the api key
    # are set AND not running in CI (traces ship prompts/completions to the
    # LangSmith cloud — a gated operation).
    langsmith_tracing: bool = False
    langsmith_api_key: SecretStr = SecretStr("")

    def is_ci(self) -> bool:
        """True when running under CI. LangSmith is force-disabled here."""
        return _env_truthy(os.environ.get("CI"))

    def sentry_enabled(self) -> bool:
        """Sentry/Spotlight is enabled ONLY when SENTRY_SPOTLIGHT is set."""
        return bool(self.sentry_spotlight.strip())

    def langsmith_enabled(self) -> bool:
        """LangSmith tracing: both env signals set AND not in CI (gated op)."""
        if self.is_ci():
            return False
        return self.langsmith_tracing and bool(self.langsmith_api_key.get_secret_value().strip())

    def chat_disabled_for(self, marketplace_account_id: str | None) -> bool:
        """Local kill-switch view (CHAT-009). Chat only; screens unaffected."""
        if self.chat_disabled_global:
            return True
        if marketplace_account_id is None:
            return False
        return marketplace_account_id in self.chat_disabled_accounts


def _env_truthy(value: str | None) -> bool:
    if value is None:
        return False
    return value.strip().lower() in {"1", "true", "yes", "on"}


def load_settings(**overrides: Any) -> Settings:
    """Load settings from the environment, allowing explicit test overrides."""
    return Settings(**overrides)

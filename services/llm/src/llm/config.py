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

from pydantic import SecretStr, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class ProviderKind(StrEnum):
    """The two OpenAI-compatible transports. Never a vendor SDK branch."""

    MOCK = "mock"
    OPENAI_COMPATIBLE = "openai_compatible"


# Default per-request deadline for a Draft write (seconds). The single source for
# the transport default (:class:`~llm.flows.gateway_draft.GatewayDraftPort` imports
# it). It MUST stay strictly below ``per_tool_timeout_seconds`` so the transport
# aborts the in-flight POST at its own deadline (httpx closes the connection, no
# ticket) BEFORE the per-tool middleware fires — enforced by ``Settings`` (#25).
DEFAULT_DRAFT_TIMEOUT_SECONDS = 10.0


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
    # Draft transport per-request deadline (seconds). MUST be strictly less than
    # ``per_tool_timeout_seconds`` (validated below): the transport must abort a
    # hung POST at its own deadline — failing closed to no ticket — before the
    # per-tool middleware fires and abandons the write on a worker thread (#25).
    draft_timeout_seconds: float = DEFAULT_DRAFT_TIMEOUT_SECONDS
    # Bounded cleanup grace after a per-tool timeout: the window the middleware
    # waits for a cancelled tool's worker to unwind before reporting it as an
    # uncancelled-worker incident (issue #25). Cooperative tools exit well within
    # this; it never becomes the containment boundary.
    per_tool_cleanup_grace_seconds: float = 1.0
    # Single §12.4 transient retry at the node level — never stacked.
    node_transient_retries: int = 1

    # --- inbound gateway authentication (issue #167) -------------------------
    # The SAME credential the Go core mints and presents as
    # ``Authorization: Bearer <token>`` on every call into this plane
    # (``LLM_GATEWAY_TOKEN``; env_prefix ``LLM_`` ⇒ this field is
    # ``gateway_token``). A SecretStr so it never renders in logs/reprs. Empty ⇒
    # no credential configured: the plane may START only under the mock provider
    # (a local-test mode; see ``validate_auth_config``), and even then inbound
    # requests fail closed unless the explicit local-test bypass below is set.
    gateway_token: SecretStr = SecretStr("")
    # Explicit, documented dev-only escape hatch: run the tokenless mock plane AND
    # accept unauthenticated inbound requests. Never valid on the production
    # (openai_compatible) transport. Off by default — fail closed.
    gateway_auth_local_bypass: bool = False

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

    @model_validator(mode="after")
    def _validate_timeout_ordering(self) -> Settings:
        """Fail closed on a Draft deadline that is not strictly before the tool bound.

        If ``draft_timeout_seconds >= per_tool_timeout_seconds`` the per-tool
        middleware could fire FIRST while the POST is still running to its own,
        later deadline — abandoning the in-flight write on a worker thread instead
        of aborting it at the transport. The ordering is a correctness invariant
        (#25), so a misordered config is rejected at load time, not tuned around.
        """
        if self.draft_timeout_seconds >= self.per_tool_timeout_seconds:
            raise ValueError(
                "draft_timeout_seconds "
                f"({self.draft_timeout_seconds}) must be strictly less than "
                f"per_tool_timeout_seconds ({self.per_tool_timeout_seconds}): the "
                "Draft transport must abort a hung POST at its own deadline before "
                "the per-tool middleware fires (issue #25)."
            )
        return self

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

    def expected_gateway_token(self) -> str | None:
        """The inbound bearer the plane requires callers to present (issue #167).

        ``None`` ⇒ no credential is configured. Startup permits that ONLY under
        the mock provider (see ``validate_auth_config``); at request time it is
        still fail-closed unless ``gateway_auth_local_bypass`` is set.
        """
        token = self.gateway_token.get_secret_value().strip()
        return token or None

    def require_gateway_auth(self) -> bool:
        """Whether inbound requests must present the gateway bearer (issue #167).

        A configured token is ALWAYS enforced. When none is configured (mock /
        local test only) the plane still fails closed — requiring auth it cannot
        satisfy — UNLESS the explicit local-test bypass is set.
        """
        if self.expected_gateway_token() is not None:
            return True
        return not self.gateway_auth_local_bypass

    def validate_auth_config(self) -> None:
        """Fail closed at startup on an unauthenticated production plane (#167).

        The production transport (``openai_compatible``) MUST carry an inbound
        gateway credential; only the mock provider may run tokenless (local
        test). The local-test bypass is never valid on the production transport.
        """
        if self.provider_kind is ProviderKind.OPENAI_COMPATIBLE:
            if self.expected_gateway_token() is None:
                raise ValueError(
                    "LLM_GATEWAY_TOKEN is required when provider_kind is "
                    "openai_compatible: the production LLM plane refuses to start "
                    "without an inbound gateway credential (issue #167)."
                )
            if self.gateway_auth_local_bypass:
                raise ValueError(
                    "gateway_auth_local_bypass is a local-test-only escape hatch "
                    "and must not be set on the openai_compatible transport (#167)."
                )

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

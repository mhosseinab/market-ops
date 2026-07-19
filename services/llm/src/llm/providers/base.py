"""The single OpenAI-compatible transport port (PRD §12.1).

``build_chat_model`` is the ONLY place a model is constructed. It dispatches on
:class:`~llm.config.ProviderKind`:

* ``MOCK`` — the deterministic in-process mock (all tests, CI; no paid calls).
* ``OPENAI_COMPATIBLE`` — ``langchain-openai``'s ``ChatOpenAI(base_url=...)``
  with base URL, credential reference, model, timeout, and token ceiling all as
  configuration.

There is NO vendor-specific SDK branch. Adding one would violate §12.1: every
provider is assumed OpenAI-compatible and reached through this one port.
"""

from __future__ import annotations

from langchain_core.language_models.chat_models import BaseChatModel

from llm.config import ProviderKind, Settings
from llm.providers.mock import MockChatModel, MockScript


def build_chat_model(settings: Settings, *, mock_script: MockScript | None = None) -> BaseChatModel:
    """Build the configured OpenAI-compatible chat model.

    ``mock_script`` is honored only for the mock provider (tests supply it).
    """
    if settings.provider_kind is ProviderKind.MOCK:
        return MockChatModel(script=mock_script or MockScript())

    if settings.provider_kind is ProviderKind.OPENAI_COMPATIBLE:
        # Imported lazily so the mock path (and CI) never needs ``langchain-openai``
        # wired to a real endpoint. The builder returns the classifying transport
        # that normalizes provider/transport failures at THIS owned boundary
        # (§12.4, issue #22) and disables the SDK's hidden retry loop so the graph
        # node stays the sole retry authority. Still the same OpenAI-compatible
        # contract — no vendor SDK branch.
        from llm.providers.openai_compatible import build_openai_compatible_model

        return build_openai_compatible_model(settings)

    # Fail closed on an unknown provider kind (defense in depth; the enum is
    # closed, so this is unreachable unless a new kind is added without wiring).
    raise ValueError(f"unsupported provider kind: {settings.provider_kind!r}")

"""The production OpenAI-compatible transport, wrapped to normalize failures.

``build_openai_compatible_model`` is the ONLY constructor of the production
``ChatOpenAI`` transport (``llm.providers.base`` calls it lazily on the
``OPENAI_COMPATIBLE`` branch, so the mock/CI path never imports
``langchain-openai``). It returns :class:`TransientClassifyingChatOpenAI`, a
thin subclass that translates raw provider/transport exceptions into the owned
:mod:`llm.providers.transient` taxonomy at the four ``BaseChatModel`` I/O choke
points (``_generate`` / ``_agenerate`` / ``_stream`` / ``_astream``) — so a
timeout, connection reset, rate limit, or 5xx surfaces as ``TransientTurnError``
(eligible for the single §12.4 node retry) and a non-retryable 4xx surfaces as
``NonRetryableProviderError`` (mapped to a structured failure, never retried).

Two invariants are enforced here:

* ``max_retries=0`` — the SDK's own hidden retry loop is DISABLED so the graph
  node stays the SOLE §12.4 retry authority (a non-zero SDK retry would silently
  multiply provider attempts and hide the transient path).
* The adapter only *classifies and re-raises*; it never itself retries and never
  swallows an error into a plausible default (fail closed).
"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator, Iterator
from typing import Any

from langchain_core.outputs import ChatGenerationChunk, ChatResult
from langchain_openai import ChatOpenAI

from llm.config import Settings
from llm.providers.transient import (
    NonRetryableProviderError,
    TransientTurnError,
    classify_provider_error,
)

_LOGGER = logging.getLogger("llm.providers")

# Stable metric/log key: a provider transport failure was classified at the owned
# boundary. Emitted (never with secrets or raw provider bodies) so telemetry can
# distinguish a contained transient/non-retryable failure from an unclassified
# escape — the classification boundary is observable, not silent.
PROVIDER_ERROR_CLASSIFIED_METRIC = "llm_provider_error_classified_total"


def _reclassify(exc: Exception) -> Exception | None:
    """Return the owned replacement for a classified provider error, else ``None``.

    ``None`` ⇒ not a provider transport failure: the caller re-raises it
    unchanged so genuine bugs are never masked as transient.
    """
    kind = classify_provider_error(exc)
    if kind is None:
        return None
    _LOGGER.warning(
        "provider_error_classified",
        extra={
            "metric": PROVIDER_ERROR_CLASSIFIED_METRIC,
            "classification": kind,
            "exception_type": type(exc).__name__,
        },
    )
    if kind == "retryable":
        return TransientTurnError(str(exc) or type(exc).__name__)
    return NonRetryableProviderError(str(exc) or type(exc).__name__)


class TransientClassifyingChatOpenAI(ChatOpenAI):
    """``ChatOpenAI`` that normalizes provider failures at the owned boundary.

    Overrides the four ``BaseChatModel`` I/O methods so classification holds on
    every path the agent uses — buffered (``invoke`` → ``_generate``) and
    streamed (``astream`` → ``_astream``). ``except Exception`` (not
    ``BaseException``) leaves ``asyncio.CancelledError`` / ``GeneratorExit``
    untouched, so client-disconnect cancellation still propagates cleanly.
    """

    def _generate(self, *args: Any, **kwargs: Any) -> ChatResult:
        try:
            return super()._generate(*args, **kwargs)
        except Exception as exc:
            replacement = _reclassify(exc)
            if replacement is not None:
                raise replacement from exc
            raise

    async def _agenerate(self, *args: Any, **kwargs: Any) -> ChatResult:
        try:
            return await super()._agenerate(*args, **kwargs)
        except Exception as exc:
            replacement = _reclassify(exc)
            if replacement is not None:
                raise replacement from exc
            raise

    def _stream(self, *args: Any, **kwargs: Any) -> Iterator[ChatGenerationChunk]:
        try:
            yield from super()._stream(*args, **kwargs)
        except Exception as exc:
            replacement = _reclassify(exc)
            if replacement is not None:
                raise replacement from exc
            raise

    async def _astream(self, *args: Any, **kwargs: Any) -> AsyncIterator[ChatGenerationChunk]:
        try:
            async for chunk in super()._astream(*args, **kwargs):
                yield chunk
        except Exception as exc:
            replacement = _reclassify(exc)
            if replacement is not None:
                raise replacement from exc
            raise


def build_openai_compatible_model(settings: Settings) -> TransientClassifyingChatOpenAI:
    """Build the production OpenAI-compatible transport (§12.1, §12.4).

    Base URL, credential, model, and per-request timeout are all configuration;
    ``max_tokens`` is the per-turn token ceiling. ``max_retries=0`` disables the
    SDK's hidden retry loop so the single §12.4 retry stays at the graph node.
    """
    return TransientClassifyingChatOpenAI(
        base_url=settings.provider_base_url,
        api_key=settings.provider_api_key,
        model=settings.provider_model,
        timeout=settings.provider_timeout_seconds,
        max_tokens=settings.max_output_tokens,  # type: ignore[call-arg]
        max_retries=0,  # the graph node is the SOLE §12.4 retry authority.
    )

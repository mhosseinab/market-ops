"""Production-shaped transient/non-retryable provider classification (issue #22).

The node-level §12.4 retry only ever sees the repository-local
``TransientTurnError``. The production ``ChatOpenAI`` transport, however, raises
the OpenAI-compatible / httpx exception hierarchy (timeouts, connection errors,
rate limits, 5xx, and non-retryable 4xx). Unless those are normalized AT THE
OWNED PROVIDER BOUNDARY, a real transient outage bypasses the single retry and
the structured ``MODEL_TRANSIENT_FAILURE`` path — it escapes as a broken stream
or a 500.

These tests exercise the SAME exception shapes the configured transport exposes
(``openai.*`` / ``httpx.*``) — never a hand-manufactured ``TransientTurnError`` —
and assert:

* explicitly-classified retryable errors surface as ``TransientTurnError`` and
  are retried EXACTLY once (two provider attempts), then fail closed to
  ``MODEL_TRANSIENT_FAILURE`` with a deep link;
* explicitly-classified non-retryable errors surface as
  ``NonRetryableProviderError``, are NOT retried (one provider attempt), and map
  to the structured ``MODEL_PROVIDER_ERROR`` failure with a deep link;
* the production model disables the SDK's own hidden retries (``max_retries=0``)
  so the graph node stays the SOLE retry authority (§12.4).

No network is touched: the parent transport's ``_generate`` is stubbed to raise
the production-shaped exception, and the classifying subclass translates it.
"""

from __future__ import annotations

from typing import Any

import httpx
import openai
import pytest
from langchain_openai import ChatOpenAI
from llm.config import ProviderKind, Settings
from llm.intents.classifier import IntentClassifier
from llm.orchestrator.agent import build_agent
from llm.orchestrator.graph import (
    NonRetryableProviderError,
    TransientTurnError,
    build_turn_graph,
)
from llm.providers.base import build_chat_model
from llm.providers.mock import MockChatModel, MockScript
from llm.providers.openai_compatible import (
    TransientClassifyingChatOpenAI,
    build_openai_compatible_model,
)
from llm.providers.transient import classify_provider_error
from llm.tools.registry import build_registry
from pydantic import SecretStr

_REQUEST = httpx.Request("POST", "http://provider.invalid/v1/chat/completions")


def _response(status: int) -> httpx.Response:
    return httpx.Response(status_code=status, request=_REQUEST)


# --- classification: production-shaped exceptions -----------------------------


@pytest.mark.parametrize(
    "exc",
    [
        openai.APITimeoutError(request=_REQUEST),
        openai.APIConnectionError(message="conn reset", request=_REQUEST),
        openai.RateLimitError("rate", response=_response(429), body=None),
        openai.InternalServerError("boom", response=_response(500), body=None),
        openai.InternalServerError("gateway", response=_response(503), body=None),
        httpx.ConnectTimeout("connect timed out"),
        httpx.ReadTimeout("read timed out"),
        httpx.ConnectError("refused"),
        httpx.RemoteProtocolError("server disconnected"),
    ],
)
def test_retryable_provider_errors_classify_retryable(exc: Exception) -> None:
    assert classify_provider_error(exc) == "retryable"


@pytest.mark.parametrize(
    "exc",
    [
        openai.BadRequestError("bad", response=_response(400), body=None),
        openai.AuthenticationError("bad key", response=_response(401), body=None),
        openai.PermissionDeniedError("forbidden", response=_response(403), body=None),
        openai.NotFoundError("no model", response=_response(404), body=None),
        openai.UnprocessableEntityError("schema", response=_response(422), body=None),
    ],
)
def test_non_retryable_provider_errors_classify_non_retryable(exc: Exception) -> None:
    assert classify_provider_error(exc) == "non_retryable"


def test_unrelated_error_is_not_classified() -> None:
    # A genuine programming error must NOT be dressed up as a provider transient
    # (it should surface, not be silently retried or contained).
    assert classify_provider_error(ValueError("bug")) is None


# --- adapter translation at the owned boundary --------------------------------


def _prod_settings() -> Settings:
    return Settings(
        provider_kind=ProviderKind.OPENAI_COMPATIBLE,
        provider_base_url="http://provider.invalid/v1",
        provider_api_key=SecretStr("test-key"),
        provider_model="test-model",
        gateway_token=SecretStr("inbound-token"),
    )


def test_build_openai_compatible_model_disables_sdk_retries() -> None:
    """The production model must set ``max_retries=0`` — the node is the sole
    retry authority (§12.4). A non-zero SDK retry would hide extra attempts."""
    model = build_openai_compatible_model(_prod_settings())
    assert isinstance(model, TransientClassifyingChatOpenAI)
    assert model.max_retries == 0
    # And the single owned port returns the classifying transport too.
    port_model = build_chat_model(_prod_settings())
    assert isinstance(port_model, TransientClassifyingChatOpenAI)
    assert port_model.max_retries == 0


def _stub_parent_generate(
    monkeypatch: pytest.MonkeyPatch, exc: Exception, calls: dict[str, int]
) -> None:
    """Make the PARENT transport's ``_generate`` raise a production-shaped error.

    The classifying subclass's ``_generate`` calls ``super()._generate`` — i.e.
    ``ChatOpenAI._generate`` — so stubbing it here drives the raw provider
    exception through the real adapter translation path (no network)."""

    def _boom(self: Any, *args: Any, **kwargs: Any) -> Any:
        calls["n"] += 1
        raise exc

    monkeypatch.setattr(ChatOpenAI, "_generate", _boom)


def test_adapter_translates_retryable_to_transient(monkeypatch: pytest.MonkeyPatch) -> None:
    calls = {"n": 0}
    _stub_parent_generate(monkeypatch, httpx.ConnectTimeout("connect timed out"), calls)
    model = build_openai_compatible_model(_prod_settings())
    with pytest.raises(TransientTurnError):
        model.invoke("hi")
    assert calls["n"] == 1


def test_adapter_translates_non_retryable_to_provider_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls = {"n": 0}
    _stub_parent_generate(
        monkeypatch,
        openai.AuthenticationError("bad key", response=_response(401), body=None),
        calls,
    )
    model = build_openai_compatible_model(_prod_settings())
    with pytest.raises(NonRetryableProviderError):
        model.invoke("hi")
    assert calls["n"] == 1


# --- end-to-end through the real agent + graph --------------------------------


def _question_classifier() -> IntentClassifier:
    """A classifier fixed to a tool-capable intent so the agent path runs."""
    return IntentClassifier(
        MockChatModel(
            script=MockScript(
                mode="answer",
                response_tool_name="IntentClassification",
                response_args={"intent": "Question", "rationale": "test"},
            )
        )
    )


def _graph_over_prod_model(settings: Settings):  # type: ignore[no-untyped-def]
    registry = build_registry()
    model = build_openai_compatible_model(settings)
    agent = build_agent(model, registry, settings)
    return build_turn_graph(agent, settings, _question_classifier())


def test_production_connection_timeout_retries_once_then_fails_closed(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A production-shaped connection timeout ⇒ EXACTLY two provider attempts,
    then the structured ``MODEL_TRANSIENT_FAILURE`` with a deep link (§12.4)."""
    calls = {"n": 0}
    _stub_parent_generate(monkeypatch, httpx.ConnectTimeout("connect timed out"), calls)
    settings = _prod_settings().model_copy(update={"node_transient_retries": 1})
    graph = _graph_over_prod_model(settings)

    result = graph.run({"message": "what changed?"})

    assert calls["n"] == 2  # one original + exactly one §12.4 node retry
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "MODEL_TRANSIENT_FAILURE"
    assert result.failure.deep_link


def test_production_non_retryable_is_not_retried_and_maps_to_structured_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A non-retryable provider error (401) ⇒ ONE attempt, no retry, and the
    explicit ``MODEL_PROVIDER_ERROR`` structured failure with a deep link."""
    calls = {"n": 0}
    _stub_parent_generate(
        monkeypatch,
        openai.AuthenticationError("bad key", response=_response(401), body=None),
        calls,
    )
    settings = _prod_settings().model_copy(update={"node_transient_retries": 1})
    graph = _graph_over_prod_model(settings)

    result = graph.run({"message": "what changed?"})

    assert calls["n"] == 1  # NOT retried
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "MODEL_PROVIDER_ERROR"
    assert result.failure.deep_link

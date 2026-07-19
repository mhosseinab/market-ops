"""Hard-bound tests (§12.4): a model that requests tools forever, a tool that
hangs, or a completion truncated at the token ceiling is stopped and fails
closed.

A mock provider in ``loop_tool`` mode always requests a tool call, forcing an
unbounded run. Each hard bound must map to the structured failure state — never
loop, hang, or silently truncate:

* graph recursion limit → ``TURN_RECURSION_LIMIT``;
* ToolCallLimitMiddleware run limits → ``TOOL_CALL_LIMIT``;
* per-tool timeout → ``TOOL_TIMEOUT``;
* token ceiling (``finish_reason=length``) → ``TOKEN_CEILING``.
"""

from __future__ import annotations

import time

from langchain_core.tools import StructuredTool
from llm.config import ProviderKind, Settings
from llm.intents.classifier import IntentClassifier
from llm.orchestrator.agent import build_agent
from llm.orchestrator.graph import build_turn_graph
from llm.providers.base import build_chat_model
from llm.providers.mock import MockChatModel, MockScript
from llm.tools.registry import build_registry
from pydantic import BaseModel, ConfigDict


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


def _loop_turn(settings: Settings):  # type: ignore[no-untyped-def]
    registry = build_registry()
    model = build_chat_model(
        settings, mock_script=MockScript(mode="loop_tool", loop_tool_name="read_observation")
    )
    agent = build_agent(model, registry, settings)
    return build_turn_graph(agent, settings, _question_classifier())


def test_recursion_limit_maps_to_structured_failure() -> None:
    # High tool-call limits so the RECURSION ceiling trips first.
    settings = Settings(
        provider_kind=ProviderKind.MOCK,
        graph_recursion_limit=6,
        tool_call_run_limit=10_000,
        per_tool_call_run_limit=10_000,
    )
    result = _loop_turn(settings).run({"message": "loop forever"})
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "TURN_RECURSION_LIMIT"
    assert result.failure.deep_link  # names the structured screen


def test_tool_call_limit_maps_to_structured_failure() -> None:
    # Low tool-call limit, high recursion, so the TOOL-CALL cap trips first.
    settings = Settings(
        provider_kind=ProviderKind.MOCK,
        graph_recursion_limit=10_000,
        tool_call_run_limit=3,
        per_tool_call_run_limit=2,
    )
    result = _loop_turn(settings).run({"message": "loop forever"})
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "TOOL_CALL_LIMIT"
    assert result.failure.deep_link


class _NoArgs(BaseModel):
    model_config = ConfigDict(extra="forbid")


def test_per_tool_timeout_maps_to_structured_failure() -> None:
    """A tool that runs past ``per_tool_timeout_seconds`` fails closed (§12.4).

    The mock loops on ``read_observation``; we swap that registry tool for one
    that sleeps well past the timeout. The per-tool-timeout middleware must stop
    waiting and raise, and the graph must map it to the ``TOOL_TIMEOUT``
    structured failure — never hang for the tool's full duration.
    """
    settings = Settings(
        provider_kind=ProviderKind.MOCK,
        graph_recursion_limit=10_000,
        tool_call_run_limit=10_000,
        per_tool_call_run_limit=10_000,
        per_tool_timeout_seconds=0.2,
        draft_timeout_seconds=0.1,  # keep the transport deadline strictly below (#25)
    )
    registry = build_registry()

    def _hang(**kwargs: object) -> dict[str, object]:
        time.sleep(3.0)  # far past the 0.2s bound
        return {"status": "never-returned"}

    registry._tools["read_observation"] = StructuredTool.from_function(  # noqa: SLF001
        func=_hang,
        name="read_observation",
        description="deliberately slow tool for the per-tool-timeout bound test",
        args_schema=_NoArgs,
    )
    model = build_chat_model(
        settings, mock_script=MockScript(mode="loop_tool", loop_tool_name="read_observation")
    )
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    started = time.monotonic()
    result = graph.run({"message": "read the observation"})
    elapsed = time.monotonic() - started

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "TOOL_TIMEOUT"
    assert result.failure.deep_link  # names the structured screen
    # It failed closed on the bound, not after the tool's full 3s runtime.
    assert elapsed < 2.0


def test_token_ceiling_maps_to_structured_failure() -> None:
    """A completion truncated at the token ceiling fails closed (§12.4).

    The mock returns a completion with ``finish_reason == "length"`` — the
    provider capped output at ``max_output_tokens``. The token-ceiling guard must
    raise instead of relaying a silently-truncated answer, and the graph must map
    it to the ``TOKEN_CEILING`` structured failure.
    """
    settings = Settings(provider_kind=ProviderKind.MOCK, max_output_tokens=8)
    registry = build_registry()
    model = build_chat_model(
        settings, mock_script=MockScript(mode="answer", finish_reason="length")
    )
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    result = graph.run({"message": "write me a very long answer"})

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "TOKEN_CEILING"
    assert result.failure.deep_link


def test_normal_completion_without_length_finish_reason_succeeds() -> None:
    """A non-truncated completion (no ``finish_reason=length``) is NOT flagged.

    Guards the token-ceiling check against firing on ordinary answers.
    """
    settings = Settings(provider_kind=ProviderKind.MOCK)
    registry = build_registry()
    model = build_chat_model(settings, mock_script=MockScript(mode="answer"))
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    result = graph.run({"message": "hello"})

    assert result.ok is True
    assert result.failure is None
    assert result.answer is not None


def test_single_transient_retry_then_failure() -> None:
    """The node retries a transient failure exactly once, then fails closed."""
    from llm.orchestrator.graph import TransientTurnError, build_turn_graph

    registry = build_registry()
    settings = Settings(provider_kind=ProviderKind.MOCK, node_transient_retries=1)
    model = build_chat_model(settings, mock_script=MockScript(mode="answer"))
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    calls = {"n": 0}
    original_invoke = agent.graph.invoke

    def failing_invoke(*args, **kwargs):  # type: ignore[no-untyped-def]
        calls["n"] += 1
        raise TransientTurnError("mock transient")

    agent.graph.invoke = failing_invoke  # type: ignore[method-assign]
    try:
        result = graph.run({"message": "hi"})
    finally:
        agent.graph.invoke = original_invoke  # type: ignore[method-assign]

    # Exactly two attempts (one original + one §12.4 retry), then structured fail.
    assert calls["n"] == 2
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "MODEL_TRANSIENT_FAILURE"

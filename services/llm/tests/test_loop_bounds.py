"""Hard-bound tests (§12.4): a model that requests tools forever is stopped.

A mock provider in ``loop_tool`` mode always requests a tool call, forcing an
unbounded run. Each hard bound (graph recursion limit, ToolCallLimitMiddleware
run limits) must stop it and map to the structured failure state — never loop
or hang.
"""

from __future__ import annotations

from llm.config import ProviderKind, Settings
from llm.orchestrator.agent import build_agent
from llm.orchestrator.graph import build_turn_graph
from llm.providers.base import build_chat_model
from llm.providers.mock import MockScript
from llm.tools.registry import build_registry


def _loop_turn(settings: Settings):  # type: ignore[no-untyped-def]
    registry = build_registry()
    model = build_chat_model(
        settings, mock_script=MockScript(mode="loop_tool", loop_tool_name="read_observation")
    )
    agent = build_agent(model, registry, settings)
    return build_turn_graph(agent, settings)


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


def test_single_transient_retry_then_failure() -> None:
    """The node retries a transient failure exactly once, then fails closed."""
    from llm.orchestrator.graph import TransientTurnError, build_turn_graph

    registry = build_registry()
    settings = Settings(provider_kind=ProviderKind.MOCK, node_transient_retries=1)
    model = build_chat_model(settings, mock_script=MockScript(mode="answer"))
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings)

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

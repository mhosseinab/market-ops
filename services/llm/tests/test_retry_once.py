"""§12.4 retry-once with a FLAKY mock: one transient failure then success.

This exercises the SAME single node-level retry S20 wired (reused, never
double-retried): a flaky agent invocation that fails transiently once and then
returns a valid structured answer must recover in exactly two attempts. A second
consecutive transient failure instead fails closed to a structured message +
deep link. No new retry mechanism is introduced.
"""

from __future__ import annotations

from typing import Any

from llm.config import Settings
from llm.envelope.models import AssistantAnswer
from llm.orchestrator.agent import AgentHandle
from llm.orchestrator.graph import TransientTurnError, build_turn_graph


class _FlakyGraph:
    """Fails transiently on the first invoke, then returns a valid answer."""

    def __init__(self, fail_times: int) -> None:
        self.calls = 0
        self._fail_times = fail_times

    def invoke(self, _inputs: Any, _config: Any) -> dict[str, Any]:  # noqa: ANN401
        self.calls += 1
        if self.calls <= self._fail_times:
            raise TransientTurnError("flaky mock transient failure")
        return {"structured_response": AssistantAnswer(summary="recovered on the retry")}


def _graph_with(fail_times: int) -> tuple[_FlakyGraph, Any]:
    settings = Settings(node_transient_retries=1)
    flaky = _FlakyGraph(fail_times=fail_times)
    agent = AgentHandle(graph=flaky, bound_tool_names=frozenset())  # type: ignore[arg-type]
    return flaky, build_turn_graph(agent, settings)


def test_one_transient_failure_then_success_recovers_in_two_attempts() -> None:
    flaky, graph = _graph_with(fail_times=1)
    result = graph.run({"message": "what changed today?"})

    assert flaky.calls == 2  # one original + exactly one §12.4 retry
    assert result.ok is True
    assert result.failure is None
    assert result.answer is not None
    assert result.answer["summary"] == "recovered on the retry"


def test_two_transient_failures_fail_closed_with_deep_link() -> None:
    flaky, graph = _graph_with(fail_times=2)
    result = graph.run({"message": "what changed today?"})

    assert flaky.calls == 2  # the retry is NOT stacked beyond one
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "MODEL_TRANSIENT_FAILURE"
    assert result.failure.deep_link  # concise failure + deep link (§12.4)

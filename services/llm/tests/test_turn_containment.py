"""The containment gate is on the LIVE turn path (B1a wiring, §12.3, CHAT-041).

Proves the wired seam, not a library nothing calls: a chat turn whose message is
an approve/confirm attempt is contained to guidance by the TurnGraph and never
reaches the agent, while a tool-capable question proceeds to the agent. Also
exercises the end-to-end /chat SSE path.
"""

from __future__ import annotations

import json
from typing import Any

from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings
from llm.envelope.models import AssistantAnswer
from llm.intents import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
from llm.metrics import ContainmentMetrics
from llm.orchestrator.agent import AgentHandle
from llm.orchestrator.graph import build_turn_graph
from llm.providers.mock import MockChatModel, MockScript


def _content_classifier() -> IntentClassifier:
    return IntentClassifier(
        MockChatModel(
            script=MockScript(
                mode="answer",
                response_tool_name="IntentClassification",
                intent_classifier=default_keyword_intent,
            )
        )
    )


class _RecordingAgent:
    def __init__(self) -> None:
        self.invoked = 0

    def invoke(self, _inputs: Any, _config: Any) -> dict[str, Any]:  # noqa: ANN401
        self.invoked += 1
        return {"structured_response": AssistantAnswer(summary="answered")}


def test_approve_turn_is_contained_and_never_reaches_agent() -> None:
    agent_impl = _RecordingAgent()
    agent = AgentHandle(graph=agent_impl, bound_tool_names=frozenset())  # type: ignore[arg-type]
    metrics = ContainmentMetrics()
    graph = build_turn_graph(agent, Settings(), _content_classifier(), metrics)

    result = graph.run({"message": "yes approve it right now"})

    assert result.ok is True
    assert result.answer is not None and "guidance" in result.answer
    assert agent_impl.invoked == 0  # guidance short-circuits BEFORE the agent
    assert metrics.total == 1
    assert metrics.by_intent.get("ApproveAction") == 1


def test_question_turn_reaches_the_agent() -> None:
    agent_impl = _RecordingAgent()
    agent = AgentHandle(graph=agent_impl, bound_tool_names=frozenset())  # type: ignore[arg-type]
    metrics = ContainmentMetrics()
    graph = build_turn_graph(agent, Settings(), _content_classifier(), metrics)

    result = graph.run({"message": "what is my margin on this product?"})

    assert result.ok is True
    assert result.answer is not None and result.answer.get("summary") == "answered"
    assert agent_impl.invoked == 1  # tool-capable intent proceeds to the agent
    assert metrics.total == 0  # no containment for a question


def test_chat_sse_contains_an_approve_message_end_to_end() -> None:
    app = create_app(Settings(provider_kind=ProviderKind.MOCK))
    with TestClient(app) as client:
        resp = client.post("/chat", json={"message": "go ahead and apply it, approved"})
        assert resp.status_code == 200
        frames = [
            json.loads(block[len("data:") :].strip())
            for block in resp.text.strip().split("\n\n")
            if block.strip().startswith("data:")
        ]

    final = next(f for f in frames if f["kind"] == "final")
    assert "guidance" in final["envelope"]
    # No approval control leaks into any frame.
    for f in frames:
        assert "approval" not in json.dumps(f).lower()
    # The containment metric fired on the live app path.
    assert app.state.app_state.metrics.total == 1

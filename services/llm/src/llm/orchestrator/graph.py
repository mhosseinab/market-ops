"""The P0 conversation turn as a LangGraph ``StateGraph``.

LangGraph is the sole top-level orchestrator (plan §4.8 amendment). S20 builds
the turn as a single-node graph that specialist agents later join as additional
nodes without re-architecture (post-P0 is multi-agent). The state holds
JSON-safe business data ONLY — never an agent/client object — so it stays
serializable and free of framework types.

Hard bounds and failure mapping (§12.4) live here:

* the embedded agent is invoked with a per-turn ``recursion_limit``; a
  ``GraphRecursionError`` maps to the structured failure;
* a ``ToolCallLimitExceededError`` (global or per-tool) maps to the same
  failure;
* exactly ONE automatic retry runs at node level for a transient error — never
  stacked with another retry mechanism.

There is NO durable checkpointer (§19.3: the LLM plane has no DB credential), so
graph state is per-request and in-process. Approval is never an interrupt: the
terminal write is at most a Draft.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, TypedDict

from langchain.agents.middleware.tool_call_limit import ToolCallLimitExceededError
from langgraph.errors import GraphRecursionError
from langgraph.graph import END, START, StateGraph

from llm.config import Settings
from llm.envelope.models import TurnFailure
from llm.orchestrator.agent import AgentHandle


class TransientTurnError(Exception):
    """A transient model/tool failure eligible for the single §12.4 retry."""


class TurnState(TypedDict, total=False):
    """JSON-safe per-turn state (no framework objects, no agent handles)."""

    message: str
    marketplace_account_id: str | None
    conversation_id: str | None
    # Outputs (exactly one of answer / failure is set when the turn ends).
    answer: dict[str, Any] | None
    failure: dict[str, Any] | None


@dataclass
class TurnResult:
    """The resolved turn: either a typed answer dict or a structured failure."""

    answer: dict[str, Any] | None
    failure: TurnFailure | None

    @property
    def ok(self) -> bool:
        return self.failure is None


_DEEP_LINK = "/app/screens"  # deterministic structured screen fallback (§12.4).


def _failure(code: str, message: str) -> dict[str, Any]:
    return TurnFailure(code=code, message=message, deep_link=_DEEP_LINK).model_dump()


class TurnGraph:
    """A compiled turn graph plus the settings that bound each run."""

    def __init__(self, compiled: Any, settings: Settings) -> None:
        self._compiled = compiled
        self._settings = settings

    def run(self, state: TurnState) -> TurnResult:
        out: TurnState = self._compiled.invoke(dict(state))
        return TurnResult(answer=out.get("answer"), failure=_as_failure(out.get("failure")))


def _as_failure(raw: dict[str, Any] | None) -> TurnFailure | None:
    if raw is None:
        return None
    return TurnFailure.model_validate(raw)


def build_turn_graph(agent: AgentHandle, settings: Settings) -> TurnGraph:
    """Build the single-node P0 turn graph around a leaf agent."""

    def agent_node(state: TurnState) -> TurnState:
        attempts = settings.node_transient_retries + 1  # one retry ⇒ two attempts
        last_transient: TransientTurnError | None = None
        for _ in range(attempts):
            try:
                inner = agent.graph.invoke(
                    {"messages": [("user", state["message"])]},
                    {"recursion_limit": settings.graph_recursion_limit},
                )
            except GraphRecursionError:
                # Unbounded loop hit the per-turn recursion ceiling.
                return {
                    "answer": None,
                    "failure": _failure(
                        "TURN_RECURSION_LIMIT",
                        "the request exceeded the per-turn step limit; use the structured screen",
                    ),
                }
            except ToolCallLimitExceededError:
                return {
                    "answer": None,
                    "failure": _failure(
                        "TOOL_CALL_LIMIT",
                        "the request exceeded the tool-call limit; use the structured screen",
                    ),
                }
            except TransientTurnError as exc:  # noqa: PERF203
                last_transient = exc
                continue

            structured = inner.get("structured_response")
            answer = structured.model_dump() if structured is not None else None
            return {"answer": answer, "failure": None}

        # Both attempts hit a transient failure (§12.4: concise message + deep link).
        message = "the assistant is temporarily unavailable; use the structured screen"
        if last_transient is not None and str(last_transient):
            message = f"{message} ({last_transient})"
        return {"answer": None, "failure": _failure("MODEL_TRANSIENT_FAILURE", message)}

    builder = StateGraph(TurnState)
    builder.add_node("agent_turn", agent_node)
    builder.add_edge(START, "agent_turn")
    builder.add_edge("agent_turn", END)
    # No checkpointer: per-request, in-process state (§19.3).
    compiled = builder.compile()
    return TurnGraph(compiled, settings)

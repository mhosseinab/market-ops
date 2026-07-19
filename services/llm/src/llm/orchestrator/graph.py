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
* a ``ToolTimeoutError`` (a single tool exceeded the per-tool timeout) maps to
  ``TOOL_TIMEOUT``;
* a ``TokenCeilingError`` (a completion truncated at ``finish_reason=length``)
  maps to ``TOKEN_CEILING`` — no silent truncation;
* exactly ONE automatic retry runs at node level for a transient error — never
  stacked with another retry mechanism.

There is NO durable checkpointer (§19.3: the LLM plane has no DB credential), so
graph state is per-request and in-process. Approval is never an interrupt: the
terminal write is at most a Draft.
"""

from __future__ import annotations

import asyncio
from collections.abc import AsyncGenerator, AsyncIterator
from dataclasses import dataclass
from typing import Any, TypedDict, cast

from langchain.agents.middleware.tool_call_limit import ToolCallLimitExceededError
from langchain_core.messages import AIMessage, AIMessageChunk
from langgraph.errors import GraphRecursionError
from langgraph.graph import END, START, StateGraph

from llm.config import Settings
from llm.envelope.models import TurnFailure
from llm.flows.dispatch import contain
from llm.intents.classifier import IntentClassifier
from llm.metrics import ContainmentMetrics
from llm.orchestrator.agent import AgentHandle, TokenCeilingError, ToolTimeoutError


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


def _turn_failure(code: str, message: str) -> TurnFailure:
    return TurnFailure(code=code, message=message, deep_link=_DEEP_LINK)


def _failure(code: str, message: str) -> dict[str, Any]:
    return _turn_failure(code, message).model_dump()


@dataclass
class TurnStreamChunk:
    """One semantic event the streamed turn yields (framework-free, JSON-safe).

    The transport (SSE) maps each to a :class:`~llm.envelope.models.ChatStreamEvent`
    frame. ``kind`` is one of ``"token"`` (an incremental free-text answer chunk —
    never a Money number), ``"final"`` (the validated envelope, produced ONLY after
    structured output completes) or ``"failure"`` (a §12.4 structured failure). No
    chunk ever carries an approval control.
    """

    kind: str
    token: str | None = None
    answer: dict[str, Any] | None = None
    failure: TurnFailure | None = None


# The §12.4 hard-bound exceptions, in the SINGLE mapping both the buffered
# (invoke) and streamed (astream) agent paths read — so a bound maps to the exact
# same structured failure however the turn is run. Order is not significant; the
# types are disjoint.
_HARD_BOUNDS: tuple[tuple[type[Exception], str, str], ...] = (
    (
        GraphRecursionError,
        "TURN_RECURSION_LIMIT",
        "the request exceeded the per-turn step limit; use the structured screen",
    ),
    (
        ToolCallLimitExceededError,
        "TOOL_CALL_LIMIT",
        "the request exceeded the tool-call limit; use the structured screen",
    ),
    (
        ToolTimeoutError,
        "TOOL_TIMEOUT",
        "a data lookup took too long; use the structured screen",
    ),
    (
        TokenCeilingError,
        "TOKEN_CEILING",
        "the response exceeded the length limit; use the structured screen",
    ),
)


def _map_hard_bound(exc: BaseException) -> TurnFailure | None:
    """Map a §12.4 hard-bound exception to its structured failure, else ``None``."""
    for exc_type, code, message in _HARD_BOUNDS:
        if isinstance(exc, exc_type):
            return _turn_failure(code, message)
    return None


def _transient_failure(last_transient: TransientTurnError | None) -> TurnFailure:
    """The §12.4 fail-closed state after the single retry is exhausted."""
    message = "the assistant is temporarily unavailable; use the structured screen"
    if last_transient is not None and str(last_transient):
        message = f"{message} ({last_transient})"
    return _turn_failure("MODEL_TRANSIENT_FAILURE", message)


@dataclass
class _Containment:
    """The deterministic pre-agent containment decision (§12.3, CHAT-041)."""

    guidance: dict[str, Any] | None = None
    failure: TurnFailure | None = None
    proceed: bool = False


def _classify_and_contain(
    classifier: IntentClassifier, metrics: ContainmentMetrics, message: str
) -> _Containment:
    """Classify a turn and contain approve/confirm attempts — the SINGLE gate.

    Shared by the buffered node and the streamed path so both apply the identical
    containment before any agent/tool/token runs: ApproveAction/ConfirmResult are
    guidance-only (no transition, no token stream); an unclassifiable turn fails
    closed; anything else proceeds to the agent (terminal write at most a Draft).
    """
    try:
        decision = classifier.classify(message)
    except ValueError:
        return _Containment(
            failure=_turn_failure(
                "INTENT_UNCLASSIFIED",
                "the assistant could not interpret the request; use the structured screen",
            )
        )
    guidance = contain(decision.intent)
    if guidance is not None:
        metrics.record_containment(decision.intent.value)
        return _Containment(guidance={"guidance": guidance.model_dump()})
    return _Containment(proceed=True)


class TurnGraph:
    """A compiled turn graph plus the settings and collaborators that bound a run.

    :meth:`run` is the buffered path (``invoke``); :meth:`astream_turn` is the
    incremental path (#23) that yields tokens AS the model produces them and the
    validated envelope only after structured output completes. Both share the one
    containment gate and the one §12.4 failure mapping.
    """

    def __init__(
        self,
        compiled: Any,
        settings: Settings,
        metrics: ContainmentMetrics,
        agent: AgentHandle,
        classifier: IntentClassifier,
    ) -> None:
        self._compiled = compiled
        self._settings = settings
        self.metrics = metrics
        self._agent = agent
        self._classifier = classifier

    def run(self, state: TurnState) -> TurnResult:
        out: TurnState = self._compiled.invoke(dict(state))
        return TurnResult(answer=out.get("answer"), failure=_as_failure(out.get("failure")))

    async def astream_turn(self, state: TurnState) -> AsyncGenerator[TurnStreamChunk, None]:
        """Stream a turn as ``token*`` then ``final`` | ``failure`` (#23).

        Containment runs FIRST (no token ever precedes it): a guidance-only intent
        yields a single ``final`` guidance chunk and NO tokens; an unclassifiable
        turn yields a structured ``failure``. Otherwise the agent is streamed, its
        free-text content forwarded as ``token`` chunks and the validated envelope
        emitted as the terminal ``final`` chunk. A mapped §12.4 failure discovered
        mid-stream still surfaces as a structured ``failure``.
        """
        outcome = _classify_and_contain(self._classifier, self.metrics, state["message"])
        if outcome.failure is not None:
            yield TurnStreamChunk(kind="failure", failure=outcome.failure)
            return
        if outcome.guidance is not None:
            yield TurnStreamChunk(kind="final", answer=outcome.guidance)
            return
        async for chunk in _astream_agent(self._agent, self._settings, state["message"]):
            yield chunk


def _as_failure(raw: dict[str, Any] | None) -> TurnFailure | None:
    if raw is None:
        return None
    return TurnFailure.model_validate(raw)


def envelope_from_structured(structured: Any) -> dict[str, Any]:
    """Serialize the agent's typed answer into the JSON-safe envelope dict that
    is placed on the SSE ``final`` frame.

    JSON mode (NOT Python mode) is mandatory here: it applies ``Money``'s
    ``field_serializer(when_used="json")`` so ``mantissa`` is already the
    signed-decimal STRING wire form (``^-?[0-9]+$``, #73 / §15.1) by the time the
    dict is handed to :meth:`ChatStreamEvent.to_sse`. A Python-mode dump would
    leave ``mantissa`` a plain ``int``; ``to_sse`` then calls ``model_dump_json``
    on the raw dict — where the field_serializer is NOT in the call graph — and
    the int re-serializes as a lossy JS-number (the exact #73 defect and the new
    web ``toMoney`` throw). Exponent stays a small integer (no precision hazard).
    """
    return cast("dict[str, Any]", structured.model_dump(mode="json"))


def build_turn_graph(
    agent: AgentHandle,
    settings: Settings,
    classifier: IntentClassifier,
    metrics: ContainmentMetrics | None = None,
) -> TurnGraph:
    """Build the P0 turn graph: classify → CONTAIN → (guidance | agent).

    Every turn passes through :func:`llm.flows.dispatch.contain` BEFORE any tool
    or agent runs. ApproveAction/ConfirmResult are answered with guidance to the
    external structured control and a free-text-containment metric — never a
    transition (§12.3, CHAT-041). Only a tool-capable intent reaches the agent,
    whose terminal write is at most a Draft.
    """
    containment_metrics = metrics if metrics is not None else ContainmentMetrics()

    def containment_node(state: TurnState) -> TurnState:
        """Classify the turn and contain approve/confirm attempts (live path)."""
        outcome = _classify_and_contain(classifier, containment_metrics, state["message"])
        if outcome.failure is not None:
            # Fail closed to the structured screen — never guess an intent (§12.4).
            return {"answer": None, "failure": outcome.failure.model_dump()}
        if outcome.guidance is not None:
            # Free text never approves: guidance-only, no transition (§12.3).
            return {"answer": outcome.guidance, "failure": None}
        # Tool-capable: proceed to the agent (terminal write is at most a Draft).
        return _run_agent(agent, settings, state)

    builder = StateGraph(TurnState)
    builder.add_node("turn", containment_node)
    builder.add_edge(START, "turn")
    builder.add_edge("turn", END)
    # No checkpointer: per-request, in-process state (§19.3).
    compiled = builder.compile()
    return TurnGraph(compiled, settings, containment_metrics, agent, classifier)


def _run_agent(agent: AgentHandle, settings: Settings, state: TurnState) -> TurnState:
    """Run the leaf agent with the §12.4 hard bounds and single transient retry."""
    attempts = settings.node_transient_retries + 1  # one retry ⇒ two attempts
    last_transient: TransientTurnError | None = None
    for _ in range(attempts):
        try:
            inner = agent.graph.invoke(
                {"messages": [("user", state["message"])]},
                {"recursion_limit": settings.graph_recursion_limit},
            )
        except TransientTurnError as exc:  # noqa: PERF203
            last_transient = exc
            continue
        except Exception as exc:  # noqa: BLE001 - remap only the known hard bounds
            failure = _map_hard_bound(exc)
            if failure is None:
                raise
            return {"answer": None, "failure": failure.model_dump()}

        structured = inner.get("structured_response")
        answer = envelope_from_structured(structured) if structured is not None else None
        return {"answer": answer, "failure": None}

    # Both attempts hit a transient failure (§12.4: concise message + deep link).
    return {"answer": None, "failure": _transient_failure(last_transient).model_dump()}


def _token_text(message: Any) -> str | None:
    """The free-text token to forward from a streamed message chunk, or ``None``.

    ONLY assistant-authored text streams as a token. A ``ToolMessage`` (including
    the structured-output echo the agent emits) and tool-call argument chunks
    (empty text — the Money-bearing structured args live there) are filtered out,
    so a token can never carry an authoritative number reconstructed from the
    stream (§9.1, #73). Non-string (multimodal) content is skipped.
    """
    if not isinstance(message, (AIMessage, AIMessageChunk)):
        return None
    content = message.content
    if isinstance(content, str) and content:
        return content
    return None


async def _astream_agent(
    agent: AgentHandle, settings: Settings, message: str
) -> AsyncIterator[TurnStreamChunk]:
    """Stream the leaf agent: forward tokens, then the validated envelope (#23).

    Uses the agent graph's async ``astream`` with combined ``updates`` +
    ``messages`` modes: ``messages`` yields model chunks (forwarded as free-text
    ``token`` events as produced — natural backpressure, no buffer), ``updates``
    carries the terminal ``structured_response`` used to build the validated
    ``final`` envelope through :func:`envelope_from_structured` (#73 string
    mantissa). The §12.4 hard bounds map to a structured ``failure``; a transient
    failure is retried exactly once — but only if it strikes BEFORE any token was
    emitted, since a partially-streamed answer cannot be cleanly re-streamed.
    Cancellation (client disconnect) closes the upstream stream and re-raises.
    """
    attempts = settings.node_transient_retries + 1
    last_transient: TransientTurnError | None = None
    for _ in range(attempts):
        emitted_token = False
        structured: Any = None
        # Combined stream modes yield ``(mode, payload)`` tuples; the Runnable
        # protocol types ``astream`` as ``AsyncIterator[dict]``, so narrow it to
        # the tuple stream we actually consume (and to the generator that exposes
        # ``aclose`` for cancellation).
        stream = cast(
            "AsyncGenerator[tuple[str, Any], None]",
            agent.graph.astream(
                {"messages": [("user", message)]},
                {"recursion_limit": settings.graph_recursion_limit},
                stream_mode=["updates", "messages"],
            ),
        )
        try:
            async for mode, chunk in stream:
                if mode == "messages":
                    msg, _meta = chunk
                    text = _token_text(msg)
                    if text is not None:
                        emitted_token = True
                        yield TurnStreamChunk(kind="token", token=text)
                elif mode == "updates":
                    for update in chunk.values():
                        if isinstance(update, dict) and update.get("structured_response"):
                            structured = update["structured_response"]
        except TransientTurnError as exc:
            last_transient = exc
            if emitted_token:
                # A partial stream already reached the client — fail closed rather
                # than re-run and double-stream (§12.4: no stacked retry).
                yield TurnStreamChunk(kind="failure", failure=_transient_failure(exc))
                return
            continue  # retry once, before any token was emitted
        except (asyncio.CancelledError, GeneratorExit):
            # Client disconnect / consumer close: stop upstream work, then re-raise.
            raise
        except Exception as exc:  # noqa: BLE001 - remap only the known hard bounds
            failure = _map_hard_bound(exc)
            if failure is None:
                raise
            yield TurnStreamChunk(kind="failure", failure=failure)
            return
        finally:
            # Bounded + cancellable: always release the upstream async stream.
            await stream.aclose()

        answer = envelope_from_structured(structured) if structured is not None else {}
        yield TurnStreamChunk(kind="final", answer=answer)
        return

    # Retries exhausted (transient before any token, twice): fail closed (§12.4).
    yield TurnStreamChunk(kind="failure", failure=_transient_failure(last_transient))

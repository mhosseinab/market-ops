"""Incremental SSE token streaming (#23, §12.1/§19.3, PRD chat P95 first-token < 3s).

The /chat SSE seam must stream model tokens AS they are produced — not buffer the
whole turn and emit a single TOKEN after ``run`` completes. These tests drive the
deterministic mock (NO paid calls, §12.5) through the async streaming path and
prove:

* a delayed multi-chunk answer yields a TOKEN frame BEFORE the run completes /
  before the FINAL frame (progressive delivery, not full-run buffering);
* the terminal envelope stays typed and validated, and a ``Money`` amount still
  serializes with a STRING mantissa on the FINAL frame (#73 must not regress);
* a mapped hard bound / transient failure discovered mid/pre-stream still surfaces
  as a STRUCTURED failure frame (fail closed, §12.4);
* free-text ApproveAction is still contained to guidance with NO token stream and
  no approval control (CHAT-041);
* client cancellation stops upstream streaming work (bounded, no runaway).
"""

from __future__ import annotations

import asyncio
import json
from typing import Any

import pytest
from fastapi.testclient import TestClient
from langchain_core.messages import AIMessage, AIMessageChunk, HumanMessage, ToolMessage
from llm.app import create_app
from llm.config import ProviderKind, Settings
from llm.envelope.models import AssistantAnswer, ChatStreamEvent, StreamEventKind
from llm.intents import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
from llm.metrics import ContainmentMetrics
from llm.orchestrator.agent import AgentHandle, build_agent
from llm.orchestrator.graph import (
    TransientTurnError,
    TurnGraph,
    TurnStreamChunk,
    _token_text,
    build_turn_graph,
)
from llm.providers.mock import MockChatModel, MockScript
from llm.tools.registry import build_registry


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


def _content_classifier() -> IntentClassifier:
    """Content-sensitive classifier so an approve message routes to containment."""
    return IntentClassifier(
        MockChatModel(
            script=MockScript(
                mode="answer",
                response_tool_name="IntentClassification",
                intent_classifier=default_keyword_intent,
            )
        )
    )


def _streaming_graph(
    *,
    stream_chunks: tuple[str, ...] = (),
    chunk_delay_seconds: float = 0.0,
    response_args: dict[str, Any] | None = None,
    settings: Settings | None = None,
    emit_counter: list[int] | None = None,
) -> TurnGraph:
    settings = settings or Settings(provider_kind=ProviderKind.MOCK)
    registry = build_registry()
    script = MockScript(
        mode="answer",
        stream_chunks=stream_chunks,
        chunk_delay_seconds=chunk_delay_seconds,
        emit_counter=emit_counter,
    )
    if response_args is not None:
        script.response_args = response_args
    model = MockChatModel(script=script)
    agent = build_agent(model, registry, settings)
    return build_turn_graph(agent, settings, _question_classifier())


async def _drain(gen: Any) -> list[TurnStreamChunk]:
    return [chunk async for chunk in gen]


# ---------------------------------------------------------------------------
# The token firewall (#73/§9.1): a direct negative test on the ONE filter that
# keeps Money-bearing structured args and the ToolMessage structured-output echo
# out of the token stream. A Money-typed boundary ⇒ mandatory negative-test-first
# (CLAUDE.md §TDD). If this filter ever forwarded a ToolMessage or a tool-call
# argument chunk, an authoritative number reconstructed from the stream could
# reach a token — exactly what these assertions forbid.
# ---------------------------------------------------------------------------


def test_token_text_drops_tool_message_structured_echo() -> None:
    """The ToolMessage structured-output echo is NEVER forwarded as a token.

    The echo's content is the repr of the structured response — it can literally
    contain the Money mantissa — so it must be dropped by type, not text.
    """
    echo = ToolMessage(
        content="Returning structured response: summary='x' amounts=[Money(mantissa=123)]",
        tool_call_id="mock-answer",
    )
    assert _token_text(echo) is None


def test_token_text_drops_tool_call_argument_chunk() -> None:
    """A tool-call argument chunk (empty text, Money-bearing args) yields no token."""
    args_chunk = AIMessageChunk(
        content="",
        tool_call_chunks=[
            {
                "name": "AssistantAnswer",
                "args": '{"amounts":[{"mantissa":123456789012345,"currency":"IRR"}]}',
                "id": "mock-answer",
                "index": 0,
            }
        ],
    )
    assert _token_text(args_chunk) is None


def test_token_text_forwards_real_assistant_text() -> None:
    """Genuine assistant free-text content IS forwarded (AIMessage and chunk)."""
    assert _token_text(AIMessage(content="hi")) == "hi"
    assert _token_text(AIMessageChunk(content="partial ")) == "partial "


def test_token_text_drops_non_ai_and_empty_and_nonstring() -> None:
    """Non-assistant, empty, and multimodal (list) content all yield no token."""
    assert _token_text(HumanMessage(content="approve it now")) is None
    assert _token_text(AIMessageChunk(content="")) is None
    assert _token_text(AIMessageChunk(content=[{"type": "image_url", "url": "x"}])) is None
    assert _token_text("not a message at all") is None


def test_chat_endpoint_emits_conversation_tokens_then_final() -> None:
    """The /chat SSE route forwards streamed tokens in order (#23, end to end).

    The app's turn graph is rebuilt on a mock that streams natural-language
    chunks, and the SSE frames must be CONVERSATION → one or more TOKEN → FINAL,
    with the streamed token text present and no approval control anywhere.
    """
    settings = Settings(provider_kind=ProviderKind.MOCK)
    app = create_app(settings)
    app.state.app_state.turn_graph = _streaming_graph(
        stream_chunks=("hello ", "there "), chunk_delay_seconds=0.01
    )
    with TestClient(app) as client:
        resp = client.post("/chat", json={"message": "what changed today?"})
        assert resp.status_code == 200
        frames = [
            json.loads(block[len("data:") :].strip())
            for block in resp.text.strip().split("\n\n")
            if block.strip().startswith("data:")
        ]
    kinds = [f["kind"] for f in frames]
    assert kinds[0] == "conversation"
    assert kinds[-1] == "final"
    assert "token" in kinds
    assert kinds.index("token") < kinds.index("final")
    tokens = "".join(f["token"] for f in frames if f["kind"] == "token")
    assert tokens == "hello there "
    for f in frames:
        assert "approval" not in json.dumps(f).lower()


@pytest.mark.asyncio
async def test_token_streams_before_run_completes() -> None:
    """A delayed multi-chunk answer delivers a TOKEN before the FINAL frame.

    The mock streams two natural-language chunks with a per-chunk async delay,
    then emits the structured answer. The FIRST item the turn stream yields must
    be a TOKEN — the model is still mid-run (blocked in the inter-chunk delay
    before producing its structured output) when that token is observed. A
    buffering implementation could only ever yield everything after ``run``.
    """
    graph = _streaming_graph(stream_chunks=("partial ", "answer "), chunk_delay_seconds=0.05)
    gen = graph.astream_turn({"message": "what changed today?", "conversation_id": "c"})

    first = await gen.__anext__()
    assert first.kind == "token"
    assert first.token == "partial "

    rest = await _drain(gen)
    kinds = [first.kind] + [c.kind for c in rest]
    # CONVERSATION is emitted by the endpoint, not the graph; here: token(s)→final.
    assert kinds[-1] == "final"
    assert kinds.count("token") >= 1
    # A token precedes the final in the emitted order (progressive delivery).
    assert kinds.index("token") < kinds.index("final")
    final = rest[-1]
    assert final.answer is not None
    assert final.answer["summary"] == "This is a deterministic mock answer."


@pytest.mark.asyncio
async def test_stream_is_not_fully_buffered() -> None:
    """The first token is observable WITHOUT draining the whole turn.

    Pulling a single item from the async generator yields a token while later
    chunks are still pending — proving frames are emitted incrementally, not
    collected into one buffer before the first emit.
    """
    graph = _streaming_graph(stream_chunks=("one ", "two ", "three "), chunk_delay_seconds=0.02)
    gen = graph.astream_turn({"message": "q", "conversation_id": "c"})
    first = await gen.__anext__()
    assert first.kind == "token"  # obtained before the final was produced
    await gen.aclose()


@pytest.mark.asyncio
async def test_final_frame_money_mantissa_is_string() -> None:
    """A Money amount still serializes with a STRING mantissa on FINAL (#73)."""
    big = 123456789012345  # > 2**53: only survives as a string on the JS wire
    graph = _streaming_graph(
        response_args={
            "summary": "the contribution figure",
            "amounts": [{"mantissa": big, "currency": "IRR", "exponent": 0}],
        },
    )
    chunks = await _drain(graph.astream_turn({"message": "price?", "conversation_id": "c"}))
    final = chunks[-1]
    assert final.kind == "final"
    assert final.answer is not None

    # The envelope goes onto the FINAL frame exactly as the endpoint emits it.
    frame = ChatStreamEvent(kind=StreamEventKind.FINAL, envelope=final.answer).to_sse()
    payload = json.loads(frame[len("data: ") :].strip())
    mantissa = payload["envelope"]["amounts"][0]["mantissa"]
    assert mantissa == str(big)
    assert isinstance(mantissa, str)
    import re

    assert re.fullmatch(r"-?[0-9]+", mantissa)


@pytest.mark.asyncio
async def test_money_mantissa_never_leaks_into_a_token_frame() -> None:
    """With real tokens flowing, the Money mantissa stays OUT of every token (#73).

    A turn whose structured answer carries a mantissa > 2**53 is driven through
    the streamed path WITH non-empty natural-language token chunks — so the
    ToolMessage structured-output echo (whose repr contains the raw int mantissa)
    is present in the underlying message stream. The token firewall must keep that
    figure out of EVERY token frame, while the FINAL frame still carries it as the
    signed-decimal STRING wire form. This is the streamed counterpart of the
    ``_token_text`` unit test: the never-cut "no authoritative number
    reconstructed from a token" guarantee, proven end to end.
    """
    big = 987654321098765  # > 2**53
    graph = _streaming_graph(
        stream_chunks=("the ", "contribution ", "is "),
        chunk_delay_seconds=0.0,
        response_args={
            "summary": "the contribution figure",
            "amounts": [{"mantissa": big, "currency": "IRR", "exponent": 0}],
        },
    )
    chunks = await _drain(graph.astream_turn({"message": "price?", "conversation_id": "c"}))

    tokens = [c for c in chunks if c.kind == "token"]
    assert tokens, "the turn must actually stream tokens for this to be meaningful"
    # The raw int repr AND the wire string must be absent from every token frame.
    for c in tokens:
        rendered = ChatStreamEvent(kind=StreamEventKind.TOKEN, token=c.token).to_sse()
        assert str(big) not in rendered
        assert "mantissa" not in rendered.lower()

    final = chunks[-1]
    assert final.kind == "final"
    assert final.answer is not None
    frame = ChatStreamEvent(kind=StreamEventKind.FINAL, envelope=final.answer).to_sse()
    payload = json.loads(frame[len("data: ") :].strip())
    mantissa = payload["envelope"]["amounts"][0]["mantissa"]
    import re

    assert mantissa == str(big)
    assert isinstance(mantissa, str)
    assert re.fullmatch(r"-?[0-9]+", mantissa)


@pytest.mark.asyncio
async def test_stream_maps_hard_bound_to_structured_failure() -> None:
    """A recursion-limit loop discovered during streaming fails closed (§12.4)."""
    settings = Settings(
        provider_kind=ProviderKind.MOCK,
        graph_recursion_limit=6,
        tool_call_run_limit=10_000,
        per_tool_call_run_limit=10_000,
    )
    registry = build_registry()
    model = MockChatModel(script=MockScript(mode="loop_tool", loop_tool_name="read_observation"))
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    chunks = await _drain(graph.astream_turn({"message": "loop forever", "conversation_id": "c"}))
    failures = [c for c in chunks if c.kind == "failure"]
    assert len(failures) == 1
    assert failures[0].failure is not None
    assert failures[0].failure.code == "TURN_RECURSION_LIMIT"
    assert failures[0].failure.deep_link  # names the structured screen


@pytest.mark.asyncio
async def test_stream_token_ceiling_maps_to_structured_failure() -> None:
    """A streamed completion truncated at the token ceiling fails closed (§12.4).

    Exercises the async ``awrap_model_call`` hook: a streamed
    ``finish_reason=length`` must raise on the astream path exactly as on invoke.
    """
    settings = Settings(provider_kind=ProviderKind.MOCK, max_output_tokens=8)
    registry = build_registry()
    model = MockChatModel(script=MockScript(mode="answer", finish_reason="length"))
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    chunks = await _drain(graph.astream_turn({"message": "long answer", "conversation_id": "c"}))
    assert chunks[-1].kind == "failure"
    assert chunks[-1].failure is not None
    assert chunks[-1].failure.code == "TOKEN_CEILING"


@pytest.mark.asyncio
async def test_stream_per_tool_timeout_maps_to_structured_failure() -> None:
    """A tool overrunning the per-tool timeout fails closed on the streamed path.

    Exercises the async ``awrap_tool_call`` hook (``asyncio.wait_for``): the guard
    must stop waiting and map to ``TOOL_TIMEOUT`` rather than hang the stream.
    """
    import time

    from langchain_core.tools import StructuredTool
    from pydantic import BaseModel, ConfigDict

    class _NoArgs(BaseModel):
        model_config = ConfigDict(extra="forbid")

    settings = Settings(
        provider_kind=ProviderKind.MOCK,
        graph_recursion_limit=10_000,
        tool_call_run_limit=10_000,
        per_tool_call_run_limit=10_000,
        per_tool_timeout_seconds=0.2,
    )
    registry = build_registry()

    def _hang(**_kwargs: object) -> dict[str, object]:
        time.sleep(3.0)  # far past the 0.2s bound
        return {"status": "never-returned"}

    registry._tools["read_observation"] = StructuredTool.from_function(  # noqa: SLF001
        func=_hang,
        name="read_observation",
        description="deliberately slow tool for the streamed per-tool-timeout bound",
        args_schema=_NoArgs,
    )
    model = MockChatModel(script=MockScript(mode="loop_tool", loop_tool_name="read_observation"))
    agent = build_agent(model, registry, settings)
    graph = build_turn_graph(agent, settings, _question_classifier())

    started = time.monotonic()
    chunks = await _drain(graph.astream_turn({"message": "read it", "conversation_id": "c"}))
    elapsed = time.monotonic() - started

    assert chunks[-1].kind == "failure"
    assert chunks[-1].failure is not None
    assert chunks[-1].failure.code == "TOOL_TIMEOUT"
    assert elapsed < 2.0  # failed on the bound, not after the tool's full runtime


class _FlakyStreamGraph:
    """An agent graph whose ``astream`` fails transiently then streams success."""

    def __init__(self, fail_times: int) -> None:
        self.calls = 0
        self._fail_times = fail_times

    def astream(self, _input: Any, _config: Any = None, *, stream_mode: Any = None) -> Any:  # noqa: ANN401
        return self._gen()

    async def _gen(self) -> Any:
        self.calls += 1
        if self.calls <= self._fail_times:
            raise TransientTurnError("flaky streaming transient")
            yield  # pragma: no cover - makes this a generator
        yield ("messages", (AIMessageChunk(content="recovered ", id="s"), {}))
        yield (
            "updates",
            {"model": {"structured_response": AssistantAnswer(summary="recovered on retry")}},
        )


@pytest.mark.asyncio
async def test_stream_transient_retries_once_then_succeeds() -> None:
    """A pre-token transient failure is retried exactly once (§12.4, not stacked)."""
    settings = Settings(provider_kind=ProviderKind.MOCK, node_transient_retries=1)
    flaky = _FlakyStreamGraph(fail_times=1)
    agent = AgentHandle(graph=flaky, bound_tool_names=frozenset())  # type: ignore[arg-type]
    graph = build_turn_graph(agent, settings, _question_classifier())

    chunks = await _drain(graph.astream_turn({"message": "q", "conversation_id": "c"}))
    assert flaky.calls == 2  # one original + exactly one retry
    assert chunks[-1].kind == "final"
    assert chunks[-1].answer is not None
    assert chunks[-1].answer["summary"] == "recovered on retry"


@pytest.mark.asyncio
async def test_stream_two_transient_failures_fail_closed() -> None:
    settings = Settings(provider_kind=ProviderKind.MOCK, node_transient_retries=1)
    flaky = _FlakyStreamGraph(fail_times=2)
    agent = AgentHandle(graph=flaky, bound_tool_names=frozenset())  # type: ignore[arg-type]
    graph = build_turn_graph(agent, settings, _question_classifier())

    chunks = await _drain(graph.astream_turn({"message": "q", "conversation_id": "c"}))
    assert flaky.calls == 2  # retry not stacked beyond one
    assert chunks[-1].kind == "failure"
    assert chunks[-1].failure is not None
    assert chunks[-1].failure.code == "MODEL_TRANSIENT_FAILURE"
    assert chunks[-1].failure.deep_link


@pytest.mark.asyncio
async def test_approve_message_streams_guidance_only_no_tokens() -> None:
    """ApproveAction is contained to guidance BEFORE any agent/token stream."""
    registry = build_registry()
    settings = Settings(provider_kind=ProviderKind.MOCK)
    model = MockChatModel(script=MockScript(mode="answer"))
    agent = build_agent(model, registry, settings)
    metrics = ContainmentMetrics()
    graph = build_turn_graph(agent, settings, _content_classifier(), metrics)

    chunks = await _drain(
        graph.astream_turn({"message": "yes approve it right now", "conversation_id": "c"})
    )
    assert [c.kind for c in chunks] == ["final"]  # NO token frames
    assert chunks[0].answer is not None and "guidance" in chunks[0].answer
    assert metrics.total == 1
    assert metrics.by_intent.get("ApproveAction") == 1
    # No approval control leaks into any streamed chunk.
    for c in chunks:
        assert "approval" not in json.dumps(c.answer or {}).lower()


@pytest.mark.asyncio
async def test_cancellation_stops_upstream_streaming() -> None:
    """Closing the turn stream stops upstream model emission (bounded, no runaway)."""
    counter: list[int] = []
    graph = _streaming_graph(
        stream_chunks=tuple(f"c{i} " for i in range(50)),
        chunk_delay_seconds=0.01,
        emit_counter=counter,
    )
    gen = graph.astream_turn({"message": "q", "conversation_id": "c"})
    await gen.__anext__()  # pull one token
    await gen.__anext__()  # pull a second token
    emitted_at_cancel = len(counter)
    await gen.aclose()  # client disconnects → upstream must stop
    await asyncio.sleep(0.1)  # far longer than several chunk delays
    # Upstream stopped: no meaningful further emission after cancellation.
    assert len(counter) - emitted_at_cancel <= 1

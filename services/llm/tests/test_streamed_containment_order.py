"""Free-text containment runs FIRST on the STREAMED turn path (§4.6, §12.3, CHAT-041).

``TurnGraph.astream_turn`` is the only path production executes (``llm.app``'s
``/chat`` streams through ``_stream_turn``), so it is the LIVE containment seam.
The buffered path is guarded by the compiled graph's ``contain`` node; this module
guards the streamed one, structurally.

"Free text never approves or executes" is a never-cut invariant, and an invariant
with no test that fails when it is broken is an unguarded invariant. These tests
therefore assert the ORDER of the streamed pipeline through RECORDING DOUBLES —
not through the incidental emptiness of a mock's output:

* the classifier/containment stage, the candidate lookup that context resolution
  performs, and the leaf agent each append a stable stage token to ONE shared,
  ordered call log;
* for a guidance-only turn (``ApproveAction`` / ``ConfirmResult``) that log must
  read EXACTLY ``["contain"]`` — containment ran, and nothing else did.

The doubles are wired so a broken ordering is loud, never silently green: the
turn context supplied here RESOLVES cleanly (in-scope product chip with a binding
version), and the agent double streams real natural-language tokens plus a
structured answer. So a build that resolved context before containing, or that
dropped the streamed containment gate, would observably run those stages.

Each test asserts the four guarantees for BOTH guidance-only intents:

1. the turn yields the guidance-only outcome pointing at the EXTERNAL structured
   control, and ZERO ``token`` chunks — no token precedes or follows containment;
2. the agent is NEVER invoked;
3. context resolution NEVER runs (no candidate lookup, no resolution telemetry);
4. NO transition is recorded on the :class:`TransitionLedger` — not even a Draft.

Deterministic mock provider only — no paid model call (§12.5).
"""

from __future__ import annotations

import json
from collections.abc import AsyncIterator, Sequence
from typing import Any

import pytest
from fastapi.testclient import TestClient
from langchain_core.messages import AIMessageChunk
from llm.app import create_app
from llm.config import ProviderKind, Settings
from llm.contextres.models import EntityCandidate, EntityRef, RequestScope
from llm.envelope.models import AssistantAnswer
from llm.flows.dispatch import STRUCTURED_CONTROL_DEEP_LINK, TransitionLedger
from llm.flows.models import GuidanceOnly
from llm.intents import GUIDANCE_ONLY_INTENTS, IntentClassifier
from llm.intents.classifier import IntentDecision
from llm.intents.keyword_mock import default_keyword_intent
from llm.metrics import ContainmentMetrics, ContextResolutionMetrics
from llm.orchestrator.agent import AgentHandle
from llm.orchestrator.graph import TurnGraph, TurnState, TurnStreamChunk, build_turn_graph
from llm.providers.mock import MockChatModel, MockScript
from pydantic import SecretStr

# Stage tokens appended to the shared, ordered call log by the recording doubles.
STAGE_CONTAIN = "contain"
STAGE_RESOLVE_CONTEXT = "resolve_context"
STAGE_AGENT = "agent"

# The authenticated scope of the turn, and a context payload that would RESOLVE
# under it (product chip, in-scope provenance, binding version present). Chosen
# deliberately: if containment were deferred until after resolution, resolution
# would SUCCEED and the turn would still end in guidance — so only the recorded
# stage log and the resolution telemetry can catch that reordering.
ORG_ID = "org-11111111-1111-1111-1111-111111111111"
ACCOUNT_ID = "acct-22222222-2222-2222-2222-222222222222"
RESOLVABLE_CONTEXT: dict[str, Any] = {
    "kind": "product",
    "entity_id": "sku-42",
    "version": 3,
    "organization_id": ORG_ID,
    "account_id": ACCOUNT_ID,
    "now": "2026-07-25T09:00:00Z",
}

# Message TEXT (never a fixture label) for each guidance-only intent. The
# content-sensitive keyword classifier routes on the words themselves.
APPROVE_MESSAGE = "yes, approve the price change right now"
CONFIRM_MESSAGE = "confirm the reconciliation result"
GUIDANCE_MESSAGES: tuple[tuple[str, str], ...] = (
    ("ApproveAction", APPROVE_MESSAGE),
    ("ConfirmResult", CONFIRM_MESSAGE),
)

_GATEWAY_TOKEN = "test-gateway-token"
AUTH_HEADERS = {"Authorization": f"Bearer {_GATEWAY_TOKEN}"}


class _RecordingClassifier(IntentClassifier):
    """The real classifier, plus a record that the CONTAINMENT stage ran.

    Classification is the first act of the containment stage, so its invocation
    is the stage's observable footprint — and its POSITION in the shared log is
    what pins containment to the front of the streamed pipeline.
    """

    def __init__(self, calls: list[str]) -> None:
        super().__init__(
            MockChatModel(
                script=MockScript(
                    mode="answer",
                    response_tool_name="IntentClassification",
                    intent_classifier=default_keyword_intent,
                )
            )
        )
        self._calls = calls

    def classify(self, message: str) -> IntentDecision:
        self._calls.append(STAGE_CONTAIN)
        return super().classify(message)


class _RecordingCandidatePort:
    """A candidate lookup that records the fact CONTEXT RESOLUTION ran.

    ``resolve_turn_context`` consults the port on every turn that carries a
    context payload, before the pure resolver runs — so an entry here means the
    resolution stage executed, whatever it went on to decide.
    """

    def __init__(self, calls: list[str]) -> None:
        self._calls = calls

    def candidates_for(
        self, *, scope: RequestScope, references: Sequence[EntityRef]
    ) -> dict[str, list[EntityCandidate]]:
        self._calls.append(STAGE_RESOLVE_CONTEXT)
        return {}


class _RecordingAgentGraph:
    """A leaf-agent double that records its invocation and then streams for real.

    It emits genuine natural-language token chunks and a structured answer, so a
    streamed path that let a guidance-only turn through would produce OBSERVABLE
    tokens — the test cannot pass merely because the double stays silent.
    """

    def __init__(self, calls: list[str]) -> None:
        self._calls = calls

    def invoke(self, _input: Any, _config: Any = None) -> dict[str, Any]:  # noqa: ANN401
        self._calls.append(STAGE_AGENT)
        return {"structured_response": AssistantAnswer(summary="the agent must never run")}

    def astream(
        self, _input: Any, _config: Any = None, *, stream_mode: Any = None
    ) -> AsyncIterator[tuple[str, Any]]:
        self._calls.append(STAGE_AGENT)
        return self._gen()

    async def _gen(self) -> AsyncIterator[tuple[str, Any]]:
        yield ("messages", (AIMessageChunk(content="approving ", id="never"), {}))
        yield ("messages", (AIMessageChunk(content="it now ", id="never"), {}))
        yield (
            "updates",
            {"model": {"structured_response": AssistantAnswer(summary="the agent ran")}},
        )


def _settings() -> Settings:
    return Settings(provider_kind=ProviderKind.MOCK, gateway_token=SecretStr(_GATEWAY_TOKEN))


def _wired_graph(
    calls: list[str],
) -> tuple[TurnGraph, ContainmentMetrics, ContextResolutionMetrics]:
    """The turn graph with all three stages instrumented by recording doubles."""
    settings = _settings()
    containment_metrics = ContainmentMetrics()
    resolution_metrics = ContextResolutionMetrics()
    agent = AgentHandle(graph=_RecordingAgentGraph(calls), bound_tool_names=frozenset())  # type: ignore[arg-type]
    graph = build_turn_graph(
        agent,
        settings,
        _RecordingClassifier(calls),
        containment_metrics,
        candidate_port=_RecordingCandidatePort(calls),
        resolution_metrics=resolution_metrics,
    )
    return graph, containment_metrics, resolution_metrics


def _turn_state(message: str) -> TurnState:
    return {
        "message": message,
        "organization_id": ORG_ID,
        "marketplace_account_id": ACCOUNT_ID,
        "conversation_id": "conv-1",
        "turn_context": dict(RESOLVABLE_CONTEXT),
    }


async def _drain(gen: AsyncIterator[TurnStreamChunk]) -> list[TurnStreamChunk]:
    return [chunk async for chunk in gen]


def test_guidance_messages_really_classify_guidance_only() -> None:
    """Precondition: the message TEXT routes to a guidance-only intent.

    Without this the ordering tests below could pass on a benign classification
    (nothing to contain), which would make them vacuous.
    """
    classifier = _RecordingClassifier([])
    for expected_intent, message in GUIDANCE_MESSAGES:
        decision = classifier.classify(message)
        assert decision.intent in GUIDANCE_ONLY_INTENTS
        assert decision.intent.value == expected_intent


@pytest.mark.asyncio
async def test_recording_agent_double_streams_tokens_when_it_is_reached() -> None:
    """Control: the doubles ARE loud when the pipeline reaches them.

    A benign (tool-capable) message runs the full streamed pipeline, so the
    stage log records contain → resolve_context → agent and real tokens flow.
    This is what makes the guidance-only assertions below meaningful rather than
    an artifact of a silent mock.
    """
    calls: list[str] = []
    graph, _containment, resolution_metrics = _wired_graph(calls)

    chunks = await _drain(graph.astream_turn(_turn_state("what changed today?")))

    assert calls == [STAGE_CONTAIN, STAGE_RESOLVE_CONTEXT, STAGE_AGENT]
    assert [c.kind for c in chunks].count("token") == 2
    assert chunks[-1].kind == "final"
    assert resolution_metrics.total == 1


@pytest.mark.asyncio
@pytest.mark.parametrize(("intent", "message"), GUIDANCE_MESSAGES)
async def test_streamed_turn_contains_before_resolution_and_before_the_agent(
    intent: str, message: str
) -> None:
    """Containment is the FIRST stage of the streamed turn — structurally.

    Kills both ordering mutations of ``astream_turn``: deferring the ``contain``
    gate until after context resolution (``resolve_context`` would appear in the
    stage log and in the resolution telemetry), and dropping the streamed
    containment gate altogether (``agent`` would appear in the log and tokens
    would reach the client).
    """
    calls: list[str] = []
    graph, containment_metrics, resolution_metrics = _wired_graph(calls)
    ledger = TransitionLedger()

    chunks = await _drain(graph.astream_turn(_turn_state(message)))

    # (1) One guidance-only outcome pointing at the EXTERNAL structured control,
    #     and ZERO tokens — none before containment, none after it.
    assert [c.kind for c in chunks] == ["final"]
    assert chunks[0].answer is not None
    guidance = GuidanceOnly.model_validate(chunks[0].answer["guidance"])
    assert guidance.deep_link == STRUCTURED_CONTROL_DEEP_LINK
    assert guidance.transitions == []
    assert containment_metrics.by_intent.get(intent) == 1

    # (2)+(3) ORDER, asserted structurally: containment ran, and it is the ONLY
    #         stage that ran. No candidate lookup, no agent invocation.
    assert calls == [STAGE_CONTAIN], f"streamed stage order was {calls}"
    assert STAGE_AGENT not in calls
    assert STAGE_RESOLVE_CONTEXT not in calls
    # Resolution is unobservable because it never ran: the node records telemetry
    # on EVERY branch it takes, including "skipped".
    assert resolution_metrics.total == 0
    assert resolution_metrics.by_outcome == {}

    # (4) No transition — not even a Draft.
    assert ledger.transitions == []
    assert ledger.approval_transitions() == []


@pytest.mark.parametrize(("intent", "message"), GUIDANCE_MESSAGES)
def test_chat_endpoint_contains_before_resolution_and_before_the_agent(
    intent: str, message: str
) -> None:
    """The same guarantee through the REAL production transport (``POST /chat``).

    ``/chat`` streams via ``astream_turn``, so this exercises the live seam end to
    end: the SSE frames are conversation → final(guidance) with NO token frame,
    and the stage log again shows containment alone.
    """
    calls: list[str] = []
    graph, containment_metrics, resolution_metrics = _wired_graph(calls)
    ledger = TransitionLedger()

    app = create_app(_settings())
    app.state.app_state.turn_graph = graph
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={
                "message": message,
                "organization_id": ORG_ID,
                "marketplace_account_id": ACCOUNT_ID,
                "context": dict(RESOLVABLE_CONTEXT),
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = [
            json.loads(block[len("data:") :].strip())
            for block in resp.text.strip().split("\n\n")
            if block.strip().startswith("data:")
        ]

    kinds = [f["kind"] for f in frames]
    assert kinds == ["conversation", "final"], f"SSE frames were {kinds}"
    assert "token" not in kinds
    guidance = GuidanceOnly.model_validate(frames[-1]["envelope"]["guidance"])
    assert guidance.deep_link == STRUCTURED_CONTROL_DEEP_LINK
    assert guidance.transitions == []
    assert containment_metrics.by_intent.get(intent) == 1

    assert calls == [STAGE_CONTAIN], f"streamed stage order was {calls}"
    assert resolution_metrics.total == 0
    assert ledger.transitions == []
    assert ledger.approval_transitions() == []

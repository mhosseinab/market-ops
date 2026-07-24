"""108b: typed turn context, deterministic resolution, canonical picker card.

NEGATIVE TESTS FIRST (CLAUDE.md TDD — this is a never-cut area):

* a provenance-less or cross-tenant active-context chip FAILS CLOSED and the
  agent is never invoked (identity quarantine, PRD §4.6 / §12);
* an ambiguous explicit reference terminates in the canonical structured picker
  with ZERO tokens, ZERO action/approval cards and ZERO Drafts (CHAT-007);
* the picker card carries no action id / parameter version / expiry / approval
  control — it is a display object, never an executable one;
* an explicit reference with the fail-closed default candidate port resolves to
  picker or not-found, NEVER to a guessed subject (the 108c seam);
* free-text containment still runs FIRST: ApproveAction/ConfirmResult terminate
  as guidance-only before any context resolution, with no transition.

Only then the happy path: a bound, in-scope chip lands on ``TurnState``.

Every test uses the deterministic mock provider — no paid model call, ever.
"""

from __future__ import annotations

import json
from typing import Any

import pytest
from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings
from llm.contextres.models import ContextType, EntityCandidate, EntityRef, RequestScope
from llm.contextres.ports import CandidateLookupError, NoCandidatePort
from llm.contextres.turn import ContextKind, TurnContext, context_kind_for
from llm.envelope.contract import PICKER_CARD_KIND, PickerCard, PickerCardOption
from llm.envelope.models import AssistantAnswer
from llm.intents import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
from llm.metrics import CONTEXT_RESOLUTION_METRIC, ContainmentMetrics, ContextResolutionMetrics
from llm.orchestrator.agent import AgentHandle
from llm.orchestrator.graph import TurnState, build_turn_graph
from llm.providers.mock import MockChatModel, MockScript
from pydantic import SecretStr, ValidationError

_GATEWAY_TOKEN = "test-gateway-token"
AUTH_HEADERS = {"Authorization": f"Bearer {_GATEWAY_TOKEN}"}

ORG = "org-1"
ACCOUNT = "acct-1"
OTHER_ORG = "org-2"
NOW = "2026-07-24T09:00:00Z"

# Deterministic keyword-mock messages, by the class they classify to.
QUESTION_MESSAGE = "what is my margin on this product?"
PREPARE_MESSAGE = "prepare a price change card for this product"
APPROVE_MESSAGE = "yes approve it right now"


def mock_settings(**overrides: Any) -> Settings:
    base: dict[str, Any] = {
        "provider_kind": ProviderKind.MOCK,
        "gateway_token": SecretStr(_GATEWAY_TOKEN),
    }
    base.update(overrides)
    return Settings(**base)


def _content_classifier() -> IntentClassifier:
    """The deterministic keyword stand-in: routes by the real message text."""
    return IntentClassifier(
        MockChatModel(
            script=MockScript(
                mode="answer",
                response_tool_name="IntentClassification",
                intent_classifier=default_keyword_intent,
            )
        )
    )


def _fixed_classifier(intent: str) -> IntentClassifier:
    """A classifier pinned to one class (the keyword mock only emits three)."""
    return IntentClassifier(
        MockChatModel(
            script=MockScript(
                mode="answer",
                response_tool_name="IntentClassification",
                intent_classifier=lambda _text: intent,
            )
        )
    )


class _RecordingAgent:
    """Stand-in leaf agent that records whether it was ever invoked."""

    def __init__(self) -> None:
        self.invoked = 0

    def invoke(self, _inputs: Any, _config: Any) -> dict[str, Any]:  # noqa: ANN401
        self.invoked += 1
        return {"structured_response": AssistantAnswer(summary="answered")}


class _FakeCandidatePort:
    """A read-only candidate supplier for tests. No write method exists."""

    def __init__(self, candidates: dict[str, list[EntityCandidate]]) -> None:
        self._candidates = candidates
        self.calls = 0

    def candidates_for(
        self, *, scope: RequestScope, references: Any
    ) -> dict[str, list[EntityCandidate]]:
        self.calls += 1
        assert isinstance(scope, RequestScope)
        return self._candidates


class _ExplodingCandidatePort:
    def candidates_for(self, *, scope: RequestScope, references: Any) -> Any:  # noqa: ANN401
        raise CandidateLookupError("gateway unavailable")


def _graph(
    agent_impl: _RecordingAgent,
    *,
    candidates: Any = None,
    resolution_metrics: ContextResolutionMetrics | None = None,
    classifier: IntentClassifier | None = None,
) -> Any:  # noqa: ANN401
    agent = AgentHandle(graph=agent_impl, bound_tool_names=frozenset())  # type: ignore[arg-type]
    return build_turn_graph(
        agent,
        Settings(),
        classifier if classifier is not None else _content_classifier(),
        ContainmentMetrics(),
        candidate_port=candidates if candidates is not None else NoCandidatePort(),
        resolution_metrics=resolution_metrics,
    )


def _state(context: dict[str, Any] | None, message: str = QUESTION_MESSAGE) -> TurnState:
    state: TurnState = {
        "message": message,
        "organization_id": ORG,
        "marketplace_account_id": ACCOUNT,
        "conversation_id": "conv-1",
    }
    if context is not None:
        state["turn_context"] = context
    return state


def _chip_context(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "kind": ContextKind.PRODUCT.value,
        "entity_id": "variant-1",
        "version": "7",
        "organization_id": ORG,
        "account_id": ACCOUNT,
        "now": NOW,
    }
    base.update(overrides)
    return base


# --- 1. identity quarantine: provenance-less / cross-tenant chip fails closed --


def test_provenance_less_chip_fails_closed_and_never_reaches_agent() -> None:
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl)

    result = graph.run(_state(_chip_context(organization_id=None, account_id=None)))

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_NOT_FOUND"
    assert result.failure.deep_link is not None
    assert agent_impl.invoked == 0  # no agent, no tokens, no fabricated subject
    assert result.answer is None


def test_cross_tenant_chip_fails_closed_and_never_relabels_into_caller_tenant() -> None:
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl)

    result = graph.run(_state(_chip_context(organization_id=OTHER_ORG)))

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_NOT_FOUND"
    assert agent_impl.invoked == 0
    # The failure never echoes the foreign tenant back to the caller.
    assert OTHER_ORG not in json.dumps(result.failure.model_dump())


def test_turn_scope_comes_from_the_request_never_from_the_context_payload() -> None:
    """A context payload can NEVER supply the turn's authenticated scope."""
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl)

    state: TurnState = {
        "message": QUESTION_MESSAGE,
        # No authenticated organization/account on the request itself...
        "turn_context": _chip_context(organization_id=OTHER_ORG, account_id="acct-9"),
    }
    result = graph.run(state)

    # ...so the turn fails closed rather than adopting the payload's tenant.
    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_SCOPE_MISSING"
    assert agent_impl.invoked == 0


# --- 2. ambiguity terminates in the structured picker -------------------------


def test_ambiguous_explicit_reference_returns_picker_with_no_cards_and_no_agent() -> None:
    agent_impl = _RecordingAgent()
    port = _FakeCandidatePort(
        {
            "sony": [
                EntityCandidate(
                    context_type=ContextType.PRODUCT,
                    entity_id="variant-1",
                    raw="sony",
                    label="Sony A",
                    organization_id=ORG,
                    account_id=ACCOUNT,
                    context_version="7",
                ),
                EntityCandidate(
                    context_type=ContextType.PRODUCT,
                    entity_id="variant-2",
                    raw="sony",
                    label="Sony B",
                    organization_id=ORG,
                    account_id=ACCOUNT,
                    context_version="8",
                ),
            ]
        }
    )
    graph = _graph(agent_impl, candidates=port)

    context = _chip_context(
        references=[{"context_type": "Product", "entity_id": "?", "raw": "sony"}]
    )
    result = graph.run(_state(context, message=PREPARE_MESSAGE))

    assert result.ok is True
    assert result.answer is not None
    cards = result.answer["cards"]
    assert len(cards) == 1
    assert cards[0]["kind"] == PICKER_CARD_KIND
    assert [o["id"] for o in cards[0]["options"]] == ["variant-1", "variant-2"]
    assert [o["label"] for o in cards[0]["options"]] == ["Sony A", "Sony B"]
    assert {o["contextKind"] for o in cards[0]["options"]} == {"product"}
    # CHAT-007: zero action/approval cards, zero Drafts, zero model invocation.
    assert agent_impl.invoked == 0
    blob = json.dumps(result.answer).lower()
    for forbidden in ("approval", "draft", "actionid", "action_id", "expires"):
        assert forbidden not in blob


def test_picker_card_is_not_an_approval_control() -> None:
    card = PickerCard(
        options=[PickerCardOption(id="v1", label="Sony", context_kind="product")]
    )
    fields = set(PickerCard.model_fields) | set(PickerCardOption.model_fields)
    forbidden = {
        "action_id",
        "actionId",
        "parameter_version",
        "parameterVersion",
        "context_version",
        "expiry",
        "expires_at",
        "approval",
        "approve",
        "confirm",
        "execute",
    }
    assert fields & forbidden == set()
    dumped = json.dumps(card.model_dump(mode="json")).lower()
    for token in ("approv", "confirm", "execut", "expir", "parameterversion", "actionid"):
        assert token not in dumped
    # Unknown fields cannot be smuggled in.
    with pytest.raises(ValidationError):
        PickerCard(
            options=[PickerCardOption(id="v1", label="Sony", context_kind="product")],
            actionId="act-1",  # type: ignore[call-arg]
        )


# --- 3. the fail-closed default candidate port (the 108c seam) ----------------


def test_default_candidate_port_supplies_nothing_and_never_guesses_a_subject() -> None:
    port = NoCandidatePort()
    assert (
        port.candidates_for(
            scope=RequestScope(organization_id=ORG, account_id=ACCOUNT),
            references=[EntityRef(context_type=ContextType.PRODUCT, entity_id="?", raw="sony")],
        )
        == {}
    )
    # Structurally read-only: no write/approve/execute/confirm method exists.
    for name in dir(port):
        assert not any(
            verb in name.lower()
            for verb in ("approve", "execute", "confirm", "draft", "write", "create")
        )


def test_explicit_reference_with_no_candidate_supply_fails_closed() -> None:
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl)

    context = _chip_context(
        references=[{"context_type": "Product", "entity_id": "?", "raw": "sony"}]
    )
    result = graph.run(_state(context, message=PREPARE_MESSAGE))

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_NOT_FOUND"
    assert agent_impl.invoked == 0
    assert result.answer is None


def test_candidate_lookup_failure_fails_closed_with_no_answer() -> None:
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl, candidates=_ExplodingCandidatePort())

    context = _chip_context(
        references=[{"context_type": "Product", "entity_id": "?", "raw": "sony"}]
    )
    result = graph.run(_state(context, message=PREPARE_MESSAGE))

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_UNAVAILABLE"
    assert agent_impl.invoked == 0


def test_card_leading_intent_on_account_level_context_never_guesses_a_target() -> None:
    """A picker with no options is not a picker — fail closed, never guess."""
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl, classifier=_fixed_classifier("PrepareAction"))

    context = _chip_context(kind=ContextKind.GLOBAL.value, entity_id=None, version=None)
    result = graph.run(_state(context, message=PREPARE_MESSAGE))

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_PICKER_UNAVAILABLE"
    assert agent_impl.invoked == 0


def test_card_leading_intent_without_a_bindable_version_fails_closed() -> None:
    """A card-leading chip missing the version a card binds never resolves (§8.1)."""
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl, classifier=_fixed_classifier("PrepareAction"))

    result = graph.run(_state(_chip_context(version=None), message=PREPARE_MESSAGE))

    assert result.ok is False
    assert result.failure is not None
    assert result.failure.code == "CONTEXT_NOT_FOUND"
    assert agent_impl.invoked == 0


def test_prepare_action_on_a_bound_versioned_chip_resolves_but_creates_no_draft() -> None:
    """The only Draft-capable class still terminates at a resolved chip here."""
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl, classifier=_fixed_classifier("PrepareAction"))

    out = graph.run_state(_state(_chip_context(), message=PREPARE_MESSAGE))

    assert out["context_resolution"]["kind"] == "resolved"
    assert out["active_context"]["entity_id"] == "variant-1"
    # 108b resolves context only: no Draft, no card, no state transition here.
    assert json.dumps(out.get("answer")).lower().count("draft") == 0


# --- 4. containment still runs FIRST -----------------------------------------


def test_containment_precedes_context_resolution() -> None:
    """An approve attempt is guidance-only BEFORE any context work runs."""
    agent_impl = _RecordingAgent()
    resolution_metrics = ContextResolutionMetrics()
    port = _FakeCandidatePort({})
    graph = _graph(agent_impl, candidates=port, resolution_metrics=resolution_metrics)

    # A chip that WOULD fail closed if resolution ran — it must not run at all.
    result = graph.run(_state(_chip_context(organization_id=OTHER_ORG), APPROVE_MESSAGE))

    assert result.ok is True
    assert result.answer is not None and "guidance" in result.answer
    assert agent_impl.invoked == 0
    assert port.calls == 0
    assert resolution_metrics.total == 0  # resolution never ran
    assert graph.metrics.by_intent.get("ApproveAction") == 1


# --- 5. happy path: a bound, in-scope chip resolves ---------------------------


def test_bound_in_scope_chip_resolves_onto_turn_state() -> None:
    agent_impl = _RecordingAgent()
    resolution_metrics = ContextResolutionMetrics()
    graph = _graph(agent_impl, resolution_metrics=resolution_metrics)

    out = graph.run_state(_state(_chip_context()))

    assert out.get("failure") is None
    assert out["active_context"] == {
        "context_type": "Product",
        "organization_id": ORG,
        "account_id": ACCOUNT,
        "entity_id": "variant-1",
        "context_version": "7",
        "recommendation_version": None,
    }
    assert out["context_resolution"]["kind"] == "resolved"
    assert out["context_resolution"]["reason"] == "active_context"
    assert agent_impl.invoked == 1  # the resolved chip grounds the agent path
    assert resolution_metrics.by_outcome.get("resolved") == 1
    assert resolution_metrics.by_reason.get("active_context") == 1


def test_absent_turn_context_skips_resolution_and_fabricates_no_subject() -> None:
    agent_impl = _RecordingAgent()
    resolution_metrics = ContextResolutionMetrics()
    graph = _graph(agent_impl, resolution_metrics=resolution_metrics)

    out = graph.run_state(_state(None))

    assert out.get("active_context") is None  # no chip is ever invented
    assert out["context_resolution"] == {"kind": "skipped", "reason": "no_turn_context"}
    assert agent_impl.invoked == 1
    assert resolution_metrics.by_outcome.get("skipped") == 1


def test_time_phrase_resolves_to_an_explicit_range_on_state() -> None:
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl)

    out = graph.run_state(_state(_chip_context(time_phrase="yesterday")))

    time_range = out["context_resolution"]["time_range"]
    assert time_range["start"] == "2026-07-23T00:00:00Z"
    assert time_range["end"] == "2026-07-24T00:00:00Z"
    assert time_range["as_of"] == NOW
    assert time_range["label_key"] == "time.range.yesterday"


# --- 6. graph state stays JSON-safe ------------------------------------------


def test_turn_state_holds_only_json_safe_business_data() -> None:
    agent_impl = _RecordingAgent()
    graph = _graph(agent_impl)

    out = graph.run_state(_state(_chip_context()))

    # No pydantic models, no framework objects, no agent handles.
    json.dumps(out)


# --- 7. the typed wire contract ----------------------------------------------


def test_turn_context_rejects_an_unknown_key_rather_than_dropping_context() -> None:
    with pytest.raises(ValidationError):
        TurnContext.model_validate({"kind": "product", "entityId": "v1"})


def test_turn_context_rejects_an_unknown_context_kind() -> None:
    with pytest.raises(ValidationError):
        TurnContext.model_validate({"kind": "not-a-kind"})


def test_context_kind_round_trips_through_the_domain_type() -> None:
    for kind in ContextKind:
        assert context_kind_for(kind.to_context_type()) is kind


def test_chat_request_carries_the_gateway_bound_context_through_verbatim() -> None:
    """The transport is a pass-through; the RESOLVER is the context validator.

    Typing ``context`` as a typed model here would make FastAPI reject a
    malformed payload with a 422 before ``resolve_turn_context`` ever ran — the
    gateway reads any non-2xx as ``provider_unavailable``, so a context contract
    mismatch would surface as "LLM plane down" with NO §12.4 structured failure
    and NO resolution telemetry. The payload therefore crosses the transport
    verbatim and is validated where the fail-closed seam lives.
    """
    from llm.app import ChatRequest

    payload = {"kind": "product", "entity_id": "v1", "version": "3"}
    req = ChatRequest.model_validate(
        {
            "message": "hi",
            "organization_id": ORG,
            "marketplace_account_id": ACCOUNT,
            # Unknown TOP-LEVEL keys stay forward-compatible (the gateway sends
            # `locale`, `user_id`, ...).
            "locale": "fa-IR",
            "context": payload,
        }
    )
    assert req.context == payload


def test_turn_context_rejects_a_typo_inside_the_context_payload() -> None:
    """``extra="forbid"`` holds: a misspelled key never silently loses the subject."""
    with pytest.raises(ValidationError):
        TurnContext.model_validate({"kind": "product", "entty_id": "v1"})


# --- 8. the wired /chat surface ----------------------------------------------


def _frames(text: str) -> list[dict[str, Any]]:
    return [
        json.loads(block[len("data:") :].strip())
        for block in text.strip().split("\n\n")
        if block.strip().startswith("data:")
    ]


def test_chat_endpoint_fails_closed_on_a_cross_tenant_context() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={
                "message": QUESTION_MESSAGE,
                "organization_id": ORG,
                "marketplace_account_id": ACCOUNT,
                "context": {
                    "kind": "product",
                    "entity_id": "v1",
                    "version": "3",
                    "organization_id": OTHER_ORG,
                    "account_id": ACCOUNT,
                },
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert [f["kind"] for f in frames if f["kind"] == "token"] == []  # zero tokens
    failure = next(f for f in frames if f["kind"] == "failure")
    assert failure["failure"]["code"] == "CONTEXT_NOT_FOUND"
    assert failure["failure"]["deep_link"]


def test_chat_endpoint_renders_the_structured_picker_for_an_ambiguous_turn() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={
                "message": PREPARE_MESSAGE,
                "organization_id": ORG,
                "marketplace_account_id": ACCOUNT,
                "context": {
                    "kind": "product",
                    "organization_id": ORG,
                    "account_id": ACCOUNT,
                    "references": [
                        {"context_type": "Product", "entity_id": "v1", "raw": "sony", "label": "A"},
                        {"context_type": "Product", "entity_id": "v2", "raw": "lg", "label": "B"},
                    ],
                },
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert [f for f in frames if f["kind"] == "token"] == []  # zero tokens
    final = next(f for f in frames if f["kind"] == "final")
    cards = final["envelope"]["cards"]
    assert cards[0]["kind"] == "picker"
    assert [o["id"] for o in cards[0]["options"]] == ["v1", "v2"]
    assert all(set(o) == {"id", "label", "contextKind"} for o in cards[0]["options"])
    # No approval control anywhere on the stream (CHAT-041, §12.3).
    for f in frames:
        assert "approval" not in json.dumps(f).lower()


def test_chat_endpoint_fails_closed_on_an_unknown_context_key_instead_of_422(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """F2: an unknown context key produces the §12.4 failure FRAME, never a 422.

    A 422 is read by the gateway (``services/core/internal/httpapi/chat.go``) as a
    transport error and surfaces as ``provider_unavailable`` — "LLM plane down"
    for a context contract mismatch, with no deep link and, worse, NO
    ``llm_context_resolution_total`` event: the fail-closed seam would be
    invisible in telemetry (CLAUDE.md: a fallback engaging without an emitted
    event is always a bug).
    """
    app = create_app(mock_settings())
    with TestClient(app) as client, caplog.at_level("INFO", logger="llm.contextres"):
        resp = client.post(
            "/chat",
            json={
                "message": QUESTION_MESSAGE,
                "organization_id": ORG,
                "marketplace_account_id": ACCOUNT,
                # `transition` is a real near-term ADDITIVE gateway key
                # (conversation.RequestedContext.Transition) the LLM plane does
                # not read yet. It must fail closed, structurally — not 422.
                "context": {
                    "kind": "product",
                    "version": 1,
                    "entity_id": "v-1",
                    "organization_id": ORG,
                    "account_id": ACCOUNT,
                    "transition": True,
                },
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert [f for f in frames if f["kind"] == "token"] == []  # zero tokens
    failure = next(f for f in frames if f["kind"] == "failure")
    assert failure["failure"]["code"] == "CONTEXT_MALFORMED"
    assert failure["failure"]["deep_link"]
    # ...and the seam is observable: the resolution event WAS emitted.
    record = next(r for r in caplog.records if r.message == "context_resolution")
    assert record.metric == CONTEXT_RESOLUTION_METRIC  # type: ignore[attr-defined]
    assert record.outcome == "not_found"  # type: ignore[attr-defined]
    assert record.reason == "turn_context_malformed"  # type: ignore[attr-defined]


def test_chat_transport_overrides_a_client_supplied_as_of_instant() -> None:
    """Freshness is the server's to assert, never the caller's (§12.3)."""
    from llm.app import _turn_context_state

    stamped = _turn_context_state({"kind": "product", "now": "1999-01-01T00:00:00Z"})
    assert stamped is not None
    assert stamped["now"] != "1999-01-01T00:00:00Z"
    assert stamped["now"].endswith("Z")


def test_chat_endpoint_still_answers_a_context_less_turn() -> None:
    """Backward compatibility: a turn with no context payload is unchanged."""
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={"message": QUESTION_MESSAGE, "organization_id": ORG},
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert any(f["kind"] == "final" for f in frames)
    assert not any(f["kind"] == "failure" for f in frames)


# --- 8b. cross-boundary contract guard: the LITERAL gateway producer shape -----
#
# Every other test in this module hand-builds its context payload. That is how F1
# shipped green: the suite invented a shape no producer emitted. These tests post
# the payload `httpLLMChat.StartTurn` actually marshals
# (`services/core/internal/httpapi/chat.go`, the `payload` / `bound` maps) and are
# the contract guard between the two planes. Mirror the producer exactly:
#   * top level: user_id, organization_id, message, conversation_id,
#     marketplace_account_id (omitted when the turn has no account), locale;
#   * context:   kind, version (a JSON **int** — conversation.ContextBinding.Version
#                is int32), entity_id (omitted when unbound), and the tenant
#                provenance organization_id / account_id read from the PERSISTED
#                conversation row — OMITTED, never zero-valued, when unrecorded.
# The gateway sends nothing else: no references, time_phrase, business_timezone,
# week_starts_on, recommendation_version or now.

GW_USER = "11111111-1111-4111-8111-111111111111"
GW_ORG = "22222222-2222-4222-8222-222222222222"
GW_ACCOUNT = "33333333-3333-4333-8333-333333333333"
GW_CONVERSATION = "44444444-4444-4444-8444-444444444444"
GW_OTHER_ORG = "55555555-5555-4555-8555-555555555555"


def _gateway_turn(
    *,
    message: str = QUESTION_MESSAGE,
    account: str | None = GW_ACCOUNT,
    context: dict[str, Any] | None,
) -> dict[str, Any]:
    """The literal JSON `httpLLMChat.StartTurn` marshals for one turn."""
    payload: dict[str, Any] = {
        "user_id": GW_USER,
        "organization_id": GW_ORG,
        "message": message,
        "conversation_id": GW_CONVERSATION,
        "locale": "fa-IR",
    }
    if account is not None:
        payload["marketplace_account_id"] = account
    if context is not None:
        payload["context"] = context
    return payload


def _gateway_bound_context(
    *,
    org: str | None = GW_ORG,
    account: str | None = GW_ACCOUNT,
    entity_id: str | None = "variant-1",
) -> dict[str, Any]:
    """The literal `bound` map, with provenance omitted when unrecorded."""
    bound: dict[str, Any] = {"kind": "product", "version": 1}
    if entity_id is not None:
        bound["entity_id"] = entity_id
    if org is not None:
        bound["organization_id"] = org
    if account is not None:
        bound["account_id"] = account
    return bound


def test_gateway_payload_without_provenance_fails_closed() -> None:
    """The PRE-fix producer shape (no tenant keys) must never resolve — this is
    the guard that would have caught F1: 100% of context-bound turns died."""
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json=_gateway_turn(context=_gateway_bound_context(org=None, account=None)),
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert [f for f in frames if f["kind"] == "token"] == []
    failure = next(f for f in frames if f["kind"] == "failure")
    assert failure["failure"]["code"] == "CONTEXT_NOT_FOUND"


def test_gateway_payload_from_a_foreign_tenant_fails_closed() -> None:
    """Stored provenance != authenticated scope: quarantine, never a relabel."""
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json=_gateway_turn(context=_gateway_bound_context(org=GW_OTHER_ORG)),
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert [f for f in frames if f["kind"] == "token"] == []
    failure = next(f for f in frames if f["kind"] == "failure")
    assert failure["failure"]["code"] == "CONTEXT_NOT_FOUND"
    assert GW_OTHER_ORG not in json.dumps(failure)


def test_gateway_payload_without_an_account_fails_closed_as_scope_missing(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A turn reaching this plane with NO account scope fails closed. Permanent.

    A context-bound turn whose request carries no ``marketplaceAccountId`` has no
    account to validate the payload's provenance against, so it fails closed with
    ``CONTEXT_SCOPE_MISSING`` / ``request_scope_missing``. This assertion is NOT a
    pinned residual — it is the invariant, and it must never be loosened: a scope
    check is only meaningful when both sides are independently sourced, and an
    account is never manufactured for a scopeless request (§4.6).

    The PRODUCTION trigger for it is closed upstream, not here: the gateway now
    derives the turn's account from the authoritative ``decision.account``
    (commit ``70fe9e1``), so a continuation that omits the field forwards the
    STORED account rather than nothing. That fixed the producer without loosening
    anything here — this plane still refuses a scopeless context-bound turn.
    """
    app = create_app(mock_settings())
    with TestClient(app) as client, caplog.at_level("INFO", logger="llm.contextres"):
        resp = client.post(
            "/chat",
            json=_gateway_turn(
                account=None,
                context=_gateway_bound_context(account=None),
            ),
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    failure = next(f for f in frames if f["kind"] == "failure")
    assert failure["failure"]["code"] == "CONTEXT_SCOPE_MISSING"
    record = next(r for r in caplog.records if r.message == "context_resolution")
    assert record.outcome == "not_found"  # type: ignore[attr-defined]
    assert record.reason == "request_scope_missing"  # type: ignore[attr-defined]


def test_gateway_payload_on_the_web_client_path_resolves(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """The REAL browser path (the dock always sends ``marketplaceAccountId``):
    a context-bound turn with matching stored provenance RESOLVES end to end."""
    app = create_app(mock_settings())
    with TestClient(app) as client, caplog.at_level("INFO", logger="llm.contextres"):
        resp = client.post(
            "/chat",
            json=_gateway_turn(context=_gateway_bound_context()),
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200
        frames = _frames(resp.text)

    assert not [f for f in frames if f["kind"] == "failure"]
    assert any(f["kind"] == "final" for f in frames)
    record = next(r for r in caplog.records if r.message == "context_resolution")
    assert record.outcome == "resolved"  # type: ignore[attr-defined]


# --- 9. observability ---------------------------------------------------------


def test_resolution_emits_stable_machine_telemetry(caplog: pytest.LogCaptureFixture) -> None:
    agent_impl = _RecordingAgent()
    metrics = ContextResolutionMetrics()
    graph = _graph(agent_impl, resolution_metrics=metrics)

    with caplog.at_level("INFO", logger="llm.contextres"):
        graph.run(_state(_chip_context(organization_id=OTHER_ORG)))

    record = next(r for r in caplog.records if r.message == "context_resolution")
    assert record.metric == CONTEXT_RESOLUTION_METRIC  # type: ignore[attr-defined]
    assert record.outcome == "not_found"  # type: ignore[attr-defined]
    assert record.reason == "organization_scope_mismatch"  # type: ignore[attr-defined]
    # No PII / tenant identifiers / free text in the diagnostic record.
    blob = json.dumps({k: str(v) for k, v in record.__dict__.items()})
    assert OTHER_ORG not in blob
    assert QUESTION_MESSAGE not in blob

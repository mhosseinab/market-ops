"""The S23 chat flows are reachable from the REAL ``POST /chat`` (issue #108, 108c).

Issue #108: the deterministic flows existed as a library and the real turn never
dispatched into them, so every read and Draft tool stayed bound to a fail-closed
stub. These tests prove the whole seam is wired, and they exercise the PRODUCTION
transport end to end — ``create_app`` → the FastAPI route → the SSE stream → the
graph's contain/resolve/dispatch nodes → the typed read adapter → the envelope.
Only the network itself is faked, at the ``httpx`` transport layer, so the
adapter, the ports, the dispatch node and the merge all run for real.

Deterministic mock provider only: no real network, no paid model call (§12.5).

Negative tests come first, deliberately: cross-tenant containment, fail-closed
behaviour on gateway errors, and "no read-only intent can reach a Draft" are
never-cut invariants (§4.6), and an invariant with no test that fails when it is
broken is an unguarded invariant.
"""

from __future__ import annotations

import json
from typing import Any

import httpx
import pytest
from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings
from llm.flows.dispatch import STRUCTURED_CONTROL_DEEP_LINK
from llm.flows.flow_dispatch import FlowKind
from llm.flows.models import DraftKind, TransitionKind
from llm.intents.models import IntentClass
from llm.tools.registry import DRAFT_TOOL_NAMES
from pydantic import SecretStr

# --- the turn's authoritative scope ------------------------------------------

ORG_ID = "org-11111111-1111-1111-1111-111111111111"
ACCOUNT_ID = "acct-22222222-2222-2222-2222-222222222222"
FOREIGN_ACCOUNT_ID = "acct-99999999-9999-9999-9999-999999999999"
VARIANT_ID = "var-33333333-3333-3333-3333-333333333333"
RECOMMENDATION_ID = "rec-44444444-4444-4444-4444-444444444444"

INBOUND_TOKEN = "inbound-gateway-token"
OUTBOUND_TOKEN = "outbound-read-draft-token"
AUTH_HEADERS = {"Authorization": f"Bearer {INBOUND_TOKEN}"}
BASE_URL = "http://core.internal"

# --- authoritative gateway fixtures ------------------------------------------
# These are the BYTES the fake gateway returns. The byte-match assertions compare
# the envelope against THESE, so a plane that re-ordered, re-derived, rounded or
# re-authored a value fails here (hazard H3).

BRIEFING_BODY: dict[str, Any] = {
    "marketplaceAccountId": ACCOUNT_ID,
    "businessDay": "2026-07-25",
    "generatedAt": "2026-07-25T04:00:00Z",
    "events": [
        {"rank": 1, "eventId": "evt-aaa", "eventType": "price_drop", "severity": "high"},
        {"rank": 2, "eventId": "evt-bbb", "eventType": "new_seller", "severity": "medium"},
        {"rank": 3, "eventId": "evt-ccc", "eventType": "buybox_lost", "severity": "low"},
    ],
}
# The authoritative ranked order. Deliberately NOT alphabetical and NOT sorted by
# any field the plane could re-derive, so a re-sort is observable.
BRIEFING_EVENT_IDS = ["evt-aaa", "evt-bbb", "evt-ccc"]

# int64 above 2^53 — a lossy JS-number/float round-trip would corrupt it (#73).
BIG_MANTISSA = "9007199254740993"
MONEY_CURRENT = {"mantissa": BIG_MANTISSA, "currency": "IRR", "exponent": 0}
MONEY_PROPOSED = {"mantissa": "-4200000", "currency": "IRR", "exponent": 0}

READINESS_BODY: dict[str, Any] = {
    "variantId": VARIANT_ID,
    "marketplaceAccountId": ACCOUNT_ID,
    "state": "partial",
    # Deliberately supplied OUT of the §9.2 AllComponents order, so a flow that
    # relayed them verbatim instead of ordering by the engine constant fails.
    "missingComponents": ["packaging", "cogs"],
    "staleComponents": ["commission"],
    "computedAt": "2026-07-25T03:00:00Z",
}
READINESS_BLOCKER_ORDER = ["cogs", "commission", "packaging"]

APPROVABLE_DETAIL: dict[str, Any] = {
    "id": RECOMMENDATION_ID,
    "marketplaceAccountId": ACCOUNT_ID,
    "variantId": VARIANT_ID,
    "lineageId": "lin-1",
    "version": 7,
    "objective": "protect_margin",
    "currentPrice": MONEY_CURRENT,
    "proposedPrice": MONEY_PROPOSED,
    "readiness": "complete",
    "evidenceQuality": "verified",
    "approvable": True,
    "simulation": False,
    "assumptions": [],
    "blockers": [],
    "contributionDeductions": [],
}
BLOCKED_DETAIL: dict[str, Any] = {
    **APPROVABLE_DETAIL,
    "approvable": False,
    # Supplied out of policy order: boundary (0) must come back before
    # hard_floor (1), which must come before cooldown (3) — CHAT-070 / §9.3.
    "blockers": [
        {"stage": "cooldown", "stageOrder": 3, "code": "cooldown_active", "message": "x"},
        {"stage": "boundary", "stageOrder": 0, "code": "boundary_unknown", "message": "x"},
        {
            "stage": "hard_floor",
            "stageOrder": 1,
            "code": "contribution_below_floor",
            "message": "x",
        },
    ],
}
BLOCKED_ORDER = ["boundary_unknown", "contribution_below_floor", "cooldown_active"]

ACTIONS_BODY: dict[str, Any] = {
    "items": [
        {
            "id": "act-1",
            "recommendationId": RECOMMENDATION_ID,
            "variantId": VARIANT_ID,
            "version": 1,
            "state": "pending_reconciliation",
            "price": MONEY_CURRENT,
            "expiresAt": "2026-07-26T00:00:00Z",
        },
        {
            "id": "act-2",
            "recommendationId": RECOMMENDATION_ID,
            "variantId": VARIANT_ID,
            "version": 1,
            "state": "accepted",
            "price": MONEY_PROPOSED,
            "expiresAt": "2026-07-26T00:00:00Z",
        },
    ],
    "hasMore": False,
}

GUARDRAILS_BODY: dict[str, Any] = {
    "marketplaceAccountId": ACCOUNT_ID,
    "settings": {"strategyEnabled": True},
    "version": 11,
    "updatedAt": "2026-07-20T00:00:00Z",
}

DRAFT_BODY: dict[str, Any] = {
    "draft_id": "draft-1",
    "action_id": "action-1",
    "context_version": "3",
    "recommendation_version": "7",
    "parameter_version": "2",
    "expires_at": "2026-07-26T00:00:00Z",
}


# --- the fake gateway ---------------------------------------------------------


class FakeGateway:
    """An ``httpx.MockTransport`` gateway that RECORDS every request it served.

    Recording is what makes "exactly once", "zero gateway calls" and "no Draft"
    provable rather than assumed: the assertions count real hits on real paths.
    """

    def __init__(self, routes: dict[str, Any], *, status: int = 200, body: Any = None) -> None:
        self._routes = routes
        self._status = status
        self._body = body
        self.hits: list[str] = []
        self.auth: list[str | None] = []

    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)

    def _handle(self, request: httpx.Request) -> httpx.Response:
        self.hits.append(request.url.path)
        self.auth.append(request.headers.get("Authorization"))
        if self._status != 200:
            return httpx.Response(self._status, json={"error": "boom"})
        if self._body is not None:
            return httpx.Response(200, content=self._body)
        payload = self._routes.get(request.url.path)
        if payload is None:
            return httpx.Response(404, json={"error": "unrouted"})
        return httpx.Response(200, json=payload)

    def count(self, path: str) -> int:
        return self.hits.count(path)


DEFAULT_ROUTES: dict[str, Any] = {
    "/briefing": BRIEFING_BODY,
    "/cost/readiness": READINESS_BODY,
    "/recommendations/detail": APPROVABLE_DETAIL,
    "/actions": ACTIONS_BODY,
    "/guardrails": GUARDRAILS_BODY,
    "/chat/cards/recommendation-draft": DRAFT_BODY,
}

DRAFT_PATH = "/chat/cards/recommendation-draft"


# --- app + turn plumbing ------------------------------------------------------


def _settings() -> Settings:
    return Settings(
        provider_kind=ProviderKind.MOCK,
        gateway_token=SecretStr(INBOUND_TOKEN),
        gateway_base_url=BASE_URL,
        gateway_outbound_token=SecretStr(OUTBOUND_TOKEN),
    )


# Message text → intent. Under the deterministic mock the default lexicon only
# distinguishes Question/Approve/Confirm, so the eight-class suite supplies its
# own stand-in. Everything except the classification runs for real.
_INTENT_MESSAGES: dict[str, IntentClass] = {
    "what should i look at today": IntentClass.QUESTION,
    "what if i moved the price": IntentClass.SIMULATION,
    "prepare the price change": IntentClass.PREPARE_ACTION,
    "how did my actions go": IntentClass.REVIEW_ACTION,
    "yes approve it now": IntentClass.APPROVE_ACTION,
    "confirm the reconciliation result": IntentClass.CONFIRM_RESULT,
    "what are my guardrails": IntentClass.ADMINISTRATION,
    "take me to the products screen": IntentClass.NAVIGATION,
}


def _classify(text: str) -> str:
    return _INTENT_MESSAGES[text.strip().lower()].value


def _client(gateway: FakeGateway) -> TestClient:
    app = create_app(
        _settings(),
        http_client=httpx.Client(transport=gateway.transport()),
        mock_intent_classifier=_classify,
    )
    return TestClient(app)


def _context(kind: str, entity_id: str | None, **extra: Any) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "kind": kind,
        "organization_id": ORG_ID,
        "account_id": ACCOUNT_ID,
        "version": 3,
    }
    if entity_id is not None:
        payload["entity_id"] = entity_id
    payload.update(extra)
    return payload


ACCOUNT_CONTEXT = _context("global", None)
PRODUCT_CONTEXT = _context("product", VARIANT_ID)
RECOMMENDATION_CONTEXT = _context(
    "recommendation", RECOMMENDATION_ID, recommendation_version=7
)
ACTION_CONTEXT = _context("action", "act-1")


def _turn(
    client: TestClient,
    message: str,
    context: dict[str, Any],
    *,
    account_id: str = ACCOUNT_ID,
) -> list[dict[str, Any]]:
    """Drive one real ``POST /chat`` turn and return its decoded SSE frames."""
    resp = client.post(
        "/chat",
        json={
            "message": message,
            "organization_id": ORG_ID,
            "marketplace_account_id": account_id,
            "context": context,
        },
        headers=AUTH_HEADERS,
    )
    assert resp.status_code == 200
    return [
        json.loads(block[len("data:") :].strip())
        for block in resp.text.strip().split("\n\n")
        if block.strip().startswith("data:")
    ]


def _final(frames: list[dict[str, Any]]) -> dict[str, Any]:
    finals = [f for f in frames if f["kind"] == "final"]
    assert finals, f"no final frame in {[f['kind'] for f in frames]}"
    envelope = finals[-1]["envelope"]
    assert isinstance(envelope, dict)
    return envelope


def _failure(frames: list[dict[str, Any]]) -> dict[str, Any]:
    failures = [f for f in frames if f["kind"] == "failure"]
    assert failures, f"no failure frame in {[f['kind'] for f in frames]}"
    failure = failures[-1]["failure"]
    assert isinstance(failure, dict)
    return failure


# =============================================================================
# NEGATIVE TESTS FIRST — the never-cut invariants (§4.6)
# =============================================================================


def test_a_model_authored_foreign_account_never_reaches_the_gateway() -> None:
    """Cross-tenant containment: the tool argument is never the scope (hazard H1).

    The registry's read tools take ``marketplace_account_id`` as a MODEL-AUTHORED
    argument. This drives the real production runner with a foreign id and proves
    (a) the runner refuses, and (b) nothing about that foreign account was ever
    put on the wire — the outbound request carries the turn's AUTHORITATIVE
    account, which came from the persisted conversation row, not the payload.
    """
    from llm.orchestrator.scope import TurnScope, turn_scope
    from llm.tools.runners import build_production_read_runners

    gateway = FakeGateway(DEFAULT_ROUTES)
    from llm.flows.gateway_read import GatewayReadPort

    port = GatewayReadPort(
        BASE_URL,
        OUTBOUND_TOKEN,
        httpx.Client(transport=gateway.transport()),
        timeout_seconds=5.0,
    )
    runners = build_production_read_runners(port, business_day="2026-07-25")
    scope = TurnScope(organization_id=ORG_ID, marketplace_account_id=ACCOUNT_ID)

    with turn_scope(scope):
        refused = runners["read_action"](marketplace_account_id=FOREIGN_ACCOUNT_ID)
    assert refused["status"] == "unavailable"
    assert refused["reason"] == "account_scope_mismatch"
    # Nothing was sent at all — the refusal happened BEFORE the transport.
    assert gateway.hits == []

    # The same runner, called honestly, scopes by the AUTHORITATIVE account.
    with turn_scope(scope):
        served = runners["read_action"]()
    assert served["status"] == "ok"
    assert gateway.count("/actions") == 1


def test_a_read_runner_without_an_authoritative_scope_fails_closed() -> None:
    """No ambient scope ⇒ no read. There is deliberately no argument fallback."""
    from llm.flows.gateway_read import GatewayReadPort
    from llm.tools.runners import build_production_read_runners

    gateway = FakeGateway(DEFAULT_ROUTES)
    port = GatewayReadPort(
        BASE_URL,
        OUTBOUND_TOKEN,
        httpx.Client(transport=gateway.transport()),
        timeout_seconds=5.0,
    )
    runners = build_production_read_runners(port, business_day="2026-07-25")
    result = runners["read_action"](marketplace_account_id=ACCOUNT_ID)
    assert result["status"] == "unavailable"
    assert result["reason"] == "no_authoritative_scope"
    assert gateway.hits == []


def test_a_model_authored_foreign_entity_never_pivots_the_subject() -> None:
    """A turn has exactly ONE active context; the model may not change it (§8.1)."""
    from llm.flows.gateway_read import GatewayReadPort
    from llm.orchestrator.scope import TurnScope, turn_scope
    from llm.tools.runners import build_production_read_runners

    gateway = FakeGateway(DEFAULT_ROUTES)
    port = GatewayReadPort(
        BASE_URL,
        OUTBOUND_TOKEN,
        httpx.Client(transport=gateway.transport()),
        timeout_seconds=5.0,
    )
    runners = build_production_read_runners(port, business_day="2026-07-25")
    scope = TurnScope(
        organization_id=ORG_ID,
        marketplace_account_id=ACCOUNT_ID,
        entity_id=VARIANT_ID,
    )
    with turn_scope(scope):
        refused = runners["read_margin"](
            marketplace_account_id=ACCOUNT_ID, entity_id="var-someone-elses"
        )
    assert refused["status"] == "unavailable"
    assert gateway.hits == []


@pytest.mark.parametrize("tool", ["read_margin", "read_policy"])
def test_an_unresolved_subject_never_enables_an_entity_scoped_read(tool: str) -> None:
    """UNKNOWN never enables: a turn with no resolved subject reads no entity.

    The resolver settles the turn's single subject (§8.1); when it settled NONE,
    the subject is UNKNOWN. An entity-scoped read must fail closed rather than
    fall back to the model-authored ``entity_id`` — that fallback would let the
    model both choose the subject and hide that it had, which is the "Unknown
    enables dependent logic" failure mode (§4.6, always a bug).
    """
    from llm.flows.gateway_read import GatewayReadPort
    from llm.orchestrator.scope import TurnScope, turn_scope
    from llm.tools.runners import build_production_read_runners

    gateway = FakeGateway(DEFAULT_ROUTES)
    port = GatewayReadPort(
        BASE_URL,
        OUTBOUND_TOKEN,
        httpx.Client(transport=gateway.transport()),
        timeout_seconds=5.0,
    )
    runners = build_production_read_runners(port, business_day="2026-07-25")
    unresolved = TurnScope(
        organization_id=ORG_ID, marketplace_account_id=ACCOUNT_ID, entity_id=None
    )
    with turn_scope(unresolved):
        refused = runners[tool](
            marketplace_account_id=ACCOUNT_ID, entity_id="var-model-chose-this"
        )
    assert refused["status"] == "unavailable"
    assert gateway.hits == [], f"an unresolved subject reached the gateway: {gateway.hits}"


def test_a_read_response_describing_another_account_fails_closed() -> None:
    """A payload whose own tenant echo is foreign is refused, never relayed."""
    from llm.flows.gateway_read import GatewayReadPort
    from llm.flows.read_ports import ReadUnavailable
    from llm.orchestrator.scope import TurnScope

    foreign = {**BRIEFING_BODY, "marketplaceAccountId": FOREIGN_ACCOUNT_ID}
    gateway = FakeGateway({"/briefing": foreign})
    port = GatewayReadPort(
        BASE_URL,
        OUTBOUND_TOKEN,
        httpx.Client(transport=gateway.transport()),
        timeout_seconds=5.0,
    )
    scope = TurnScope(organization_id=ORG_ID, marketplace_account_id=ACCOUNT_ID)
    with pytest.raises(ReadUnavailable):
        port.briefing(scope, business_day="2026-07-25")


@pytest.mark.parametrize(
    ("label", "gateway"),
    [
        ("gateway_5xx", FakeGateway(DEFAULT_ROUTES, status=503)),
        ("gateway_4xx", FakeGateway(DEFAULT_ROUTES, status=404)),
        ("malformed_body", FakeGateway(DEFAULT_ROUTES, body=b"{not json")),
        ("short_body", FakeGateway({"/briefing": {"businessDay": "2026-07-25"}})),
    ],
)
def test_gateway_failures_fail_closed_with_no_fabricated_answer(
    label: str, gateway: FakeGateway
) -> None:
    """A read that cannot be served fails the turn CLOSED (§12.4).

    No answer frame, no card, no Draft — and the failure carries the deterministic
    screens deep link. A plausible-looking guess would be the exact degradation
    §12.2 forbids.
    """
    with _client(gateway) as client:
        frames = _turn(client, "what should i look at today", ACCOUNT_CONTEXT)

    assert [f["kind"] for f in frames] == ["conversation", "failure"], label
    failure = _failure(frames)
    assert failure["code"] == "CONTEXT_UNAVAILABLE"
    assert failure["deep_link"] == "/app/today"
    # Nothing was drafted, and no answer content reached the client.
    assert gateway.count(DRAFT_PATH) == 0
    assert not [f for f in frames if f["kind"] in ("final", "token")]


def test_a_transport_error_fails_closed_and_drafts_nothing() -> None:
    """A connection failure is a closed read, never an empty-but-successful one."""

    def explode(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("gateway unreachable")

    app = create_app(
        _settings(),
        http_client=httpx.Client(transport=httpx.MockTransport(explode)),
        mock_intent_classifier=_classify,
    )
    with TestClient(app) as client:
        frames = _turn(client, "prepare the price change", RECOMMENDATION_CONTEXT)
    assert _failure(frames)["code"] == "CONTEXT_UNAVAILABLE"
    assert not [f for f in frames if f["kind"] == "final"]


@pytest.mark.parametrize(
    ("message", "context"),
    [
        ("what should i look at today", ACCOUNT_CONTEXT),
        ("what if i moved the price", RECOMMENDATION_CONTEXT),
        ("how did my actions go", ACTION_CONTEXT),
        ("what are my guardrails", ACCOUNT_CONTEXT),
        ("take me to the products screen", PRODUCT_CONTEXT),
    ],
)
def test_no_read_only_intent_can_reach_the_draft_endpoint(
    message: str, context: dict[str, Any]
) -> None:
    """Simulation and every read-only intent originate NOTHING (§8.2, #31).

    Asserted twice over, structurally: the agent bound to the intent holds no
    Draft tool at all, AND the Draft endpoint records zero hits for the turn.
    """
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        state = client.app.state.app_state  # type: ignore[attr-defined]
        frames = _turn(client, message, context)
        intent = _INTENT_MESSAGES[message]
        bound = state.agents_by_intent[intent.value].bound_tool_names
        assert not (bound & DRAFT_TOOL_NAMES), f"{intent} may bind a Draft tool"

    assert gateway.count(DRAFT_PATH) == 0
    envelope = _final(frames)
    assert envelope.get("flow", {}).get("draft") is None
    assert envelope.get("flow", {}).get("transitions") == []


@pytest.mark.parametrize(
    "message", ["yes approve it now", "confirm the reconciliation result"]
)
def test_guidance_only_intents_read_nothing_and_draft_nothing(message: str) -> None:
    """Free text never approves — and now never READS either (§12.3, CHAT-041).

    Containment still runs FIRST, before the new dispatch node, so an
    approve/confirm turn makes ZERO gateway calls: no briefing, no detail, no
    Draft. That the gateway recorded nothing at all is the structural proof the
    dispatcher did not perturb the containment ordering (hazard H4).
    """
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, message, RECOMMENDATION_CONTEXT)

    assert [f["kind"] for f in frames] == ["conversation", "final"]
    guidance = _final(frames)["guidance"]
    assert guidance["deep_link"] == STRUCTURED_CONTROL_DEEP_LINK
    assert guidance["transitions"] == []
    assert gateway.hits == [], f"a contained turn touched the gateway: {gateway.hits}"


def test_the_outbound_credential_is_presented_and_never_the_inbound_one() -> None:
    """Outbound calls carry the read/Draft-only credential, not the inbound token."""
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        _turn(client, "what should i look at today", ACCOUNT_CONTEXT)
    assert gateway.auth
    assert all(header == f"Bearer {OUTBOUND_TOKEN}" for header in gateway.auth)
    assert all(INBOUND_TOKEN not in (header or "") for header in gateway.auth)


# =============================================================================
# THE EIGHT-CLASS DISPATCH CONTRACT (PD-4)
# =============================================================================


def test_question_without_entity_context_dispatches_briefing() -> None:
    """Question + account scope ⇒ briefing, byte-matching the authoritative feed.

    The event ids AND their order equal the gateway's bytes exactly (CHAT-010):
    the plane never re-ranks, filters, or drops an item.
    """
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "what should i look at today", ACCOUNT_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.BRIEFING.value
    assert flow["event_ids"] == BRIEFING_EVENT_IDS
    assert gateway.count("/briefing") == 1


def test_question_with_entity_context_dispatches_investigation_with_blockers() -> None:
    """Question + product chip ⇒ investigation, blockers in CHAT-070 engine order.

    The fixture supplies the components OUT of order on purpose, so a flow that
    relayed them verbatim instead of mirroring the §9.2 ``AllComponents`` constant
    fails here.
    """
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "what should i look at today", PRODUCT_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.INVESTIGATION.value
    assert [b["code"] for b in flow["blockers"]] == READINESS_BLOCKER_ORDER
    # One blocker at a time (CHAT-071): the first in policy order.
    assert flow["next_blocker_code"] == READINESS_BLOCKER_ORDER[0]
    assert gateway.count("/cost/readiness") == 1


def test_simulation_dispatches_a_non_executable_simulation() -> None:
    """Simulation ⇒ labelled non-executable, no Draft, no transition (CHAT-032)."""
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "what if i moved the price", RECOMMENDATION_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.SIMULATION.value
    assert flow["simulation"] is True
    assert flow["draft"] is None
    assert flow["transitions"] == []
    assert gateway.count(DRAFT_PATH) == 0


def test_prepare_action_reaches_the_draft_endpoint_exactly_once() -> None:
    """Prepare Action ⇒ ONE Draft, terminal at Draft (§8.2, hazard H2).

    "Exactly once" is structural, not incidental: the deterministic flow is the
    ONLY Draft origination path, because the registry's production seam refuses to
    wire a DRAFT tool, so the model-visible ``draft_*`` tools have no transport at
    all. The endpoint hit count proves it.
    """
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "prepare the price change", RECOMMENDATION_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.PREPARE_ACTION.value
    assert gateway.count(DRAFT_PATH) == 1, gateway.hits

    draft = flow["draft"]
    assert draft is not None
    assert draft["draft_kind"] == DraftKind.RECOMMENDATION.value
    # Bound at creation (§8.1) from the AUTHORITATIVE read, not from the model.
    assert draft["account_id"] == ACCOUNT_ID
    assert draft["entity_id"] == VARIANT_ID
    assert draft["context_version"] == "3"
    # Terminal at Draft: the ONLY transition the plane can ever record.
    assert flow["transitions"] == [TransitionKind.DRAFT.value]
    # The card's confirmation is an EXTERNAL control, only deep-linked to.
    assert draft["control_deep_link"].startswith("/app/actions?card=")


def test_prepare_action_on_a_blocked_target_returns_guidance_not_a_draft() -> None:
    """A blocked Prepare Action gets blocker guidance, never a refused Draft (PD-4).

    Composite blocker entry, third form: one blocker at a time in CHAT-070 policy
    order, and NOTHING is originated.
    """
    gateway = FakeGateway({**DEFAULT_ROUTES, "/recommendations/detail": BLOCKED_DETAIL})
    with _client(gateway) as client:
        frames = _turn(client, "prepare the price change", RECOMMENDATION_CONTEXT)

    flow = _final(frames)["flow"]
    assert [b["code"] for b in flow["blockers"]] == BLOCKED_ORDER
    assert flow["next_blocker_code"] == BLOCKED_ORDER[0]
    assert flow["draft"] is None
    assert flow["transitions"] == []
    assert gateway.count(DRAFT_PATH) == 0


def test_review_action_dispatches_monitoring() -> None:
    """ReviewAction ⇒ monitoring: read-only action state grouped by §8.4 state."""
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "how did my actions go", ACTION_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.MONITORING.value
    assert flow["action_groups"] == {
        "pending_reconciliation": ["act-1"],
        "accepted": ["act-2"],
    }
    assert gateway.count("/actions") == 1
    assert gateway.count(DRAFT_PATH) == 0


def test_administration_dispatches_a_level1_read_and_a_settings_deep_link() -> None:
    """Administration ⇒ L1 read + explanation + deep link. No Draft (§8.3, CHAT-062)."""
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "what are my guardrails", ACCOUNT_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.ADMINISTRATION.value
    assert flow["deep_link"] == "/app/settings?section=guardrails"
    assert flow["draft"] is None
    assert gateway.count("/guardrails") == 1
    assert gateway.count(DRAFT_PATH) == 0


def test_navigation_dispatches_a_deterministic_deep_link_and_reads_nothing() -> None:
    """Navigation ⇒ the closed route map alone: no card, no Draft, no read."""
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "take me to the products screen", PRODUCT_CONTEXT)

    flow = _final(frames)["flow"]
    assert flow["flow"] == FlowKind.NAVIGATION.value
    assert flow["deep_link"] == "/app/products"
    assert gateway.hits == []


# =============================================================================
# BYTE-MATCH: the model composes prose, never the numbers (hazard H3)
# =============================================================================


def test_engine_money_byte_matches_the_gateway_and_never_becomes_a_float() -> None:
    """Money is relayed EXACTLY, in the signed-decimal STRING wire form (§9.1, #73).

    The fixture mantissa is above 2^53, so any float/JS-number round trip on the
    path would corrupt it observably. The envelope's ``amounts`` is REPLACED by
    the engine figures, so a model-authored number cannot survive the merge.
    """
    gateway = FakeGateway(DEFAULT_ROUTES)
    with _client(gateway) as client:
        frames = _turn(client, "prepare the price change", RECOMMENDATION_CONTEXT)

    envelope = _final(frames)
    assert envelope["amounts"] == [MONEY_CURRENT, MONEY_PROPOSED]
    assert envelope["flow"]["amounts"] == [MONEY_CURRENT, MONEY_PROPOSED]
    # The mantissa crossed the SSE boundary as a string, byte-identical.
    raw = [f for f in frames if f["kind"] == "final"][-1]
    assert BIG_MANTISSA in json.dumps(raw)


def test_deterministic_facts_overwrite_whatever_the_model_authored() -> None:
    """The merge is one-directional: authoritative facts win, always.

    A model that emitted its own ``amounts`` has them DISCARDED — not merged, not
    reconciled — because every numeric financial value must come from an engine
    output (§12.3).
    """
    from llm.flows.flow_dispatch import apply_flow_facts

    model_answer = {"summary": "I think it is about five million", "amounts": [MONEY_PROPOSED]}
    facts = {"flow": "briefing", "amounts": [], "event_ids": BRIEFING_EVENT_IDS}
    merged = apply_flow_facts(model_answer, facts)
    assert merged is not None
    assert merged["amounts"] == []
    assert merged["flow"]["event_ids"] == BRIEFING_EVENT_IDS
    # The model's own natural-language slot is untouched.
    assert merged["summary"] == "I think it is about five million"


# =============================================================================
# CONCURRENCY: two turns never observe each other's scope (hazard H1)
# =============================================================================


@pytest.mark.asyncio
async def test_concurrent_turns_do_not_leak_each_others_scope() -> None:
    """The scope binding is per-Task, so parallel SSE turns stay isolated."""
    import asyncio

    from llm.orchestrator.scope import TurnScope, current_turn_scope, turn_scope

    observed: dict[str, str | None] = {}

    async def run(account: str) -> None:
        scope = TurnScope(organization_id=ORG_ID, marketplace_account_id=account)
        with turn_scope(scope):
            await asyncio.sleep(0)  # force interleaving
            seen = current_turn_scope()
            observed[account] = seen.marketplace_account_id if seen else None

    await asyncio.gather(run(ACCOUNT_ID), run(FOREIGN_ACCOUNT_ID))
    assert observed == {ACCOUNT_ID: ACCOUNT_ID, FOREIGN_ACCOUNT_ID: FOREIGN_ACCOUNT_ID}
    # And nothing leaked out of either block.
    assert current_turn_scope() is None

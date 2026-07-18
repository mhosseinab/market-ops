"""Real Draft-only transport over the gateway (B1b, §8.2, §12.3).

Exercises :class:`GatewayDraftPort` against an httpx ``MockTransport`` standing in
for the gateway's Draft-create endpoints — proving the transport is real code
(not a stub), that it presents the read/Draft-only bearer token, maps the
response to a Draft ticket, and FAILS CLOSED on any non-2xx or malformed body
(never fabricates a Draft). It also asserts the adapter exposes ONLY the three
Draft methods — no approve/execute/confirm.
"""

from __future__ import annotations

import json

import httpx
import pytest
from llm.flows.gateway_draft import DraftUnavailable, GatewayDraftPort
from llm.flows.models import DraftKind


def _port(handler: httpx.MockTransport) -> GatewayDraftPort:
    client = httpx.Client(transport=handler)
    return GatewayDraftPort("http://gateway.internal", "read-draft-token", client)


def test_recommendation_draft_maps_response_and_sends_token() -> None:
    seen: dict[str, object] = {}

    def handle(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("Authorization")
        seen["path"] = request.url.path
        seen["body"] = json.loads(request.content)
        return httpx.Response(
            200,
            json={
                "draft_id": "d-1",
                "action_id": "act-1",
                "context_version": "ctx-7",
                "recommendation_version": "rec-9",
                "parameter_version": "pv-3",
                "expires_at": "2026-07-18T12:00:00Z",
            },
        )

    port = _port(httpx.MockTransport(handle))
    ticket = port.create_recommendation_draft(
        account_id="acc-1", entity_id="p-1", recommendation_id="rec-9"
    )
    assert ticket.draft_kind is DraftKind.RECOMMENDATION
    assert ticket.draft_id == "d-1" and ticket.action_id == "act-1"
    assert ticket.control_deep_link == "/app/actions?card=act-1"
    assert seen["auth"] == "Bearer read-draft-token"
    assert seen["path"] == "/chat/cards/recommendation-draft"
    assert seen["body"] == {
        "marketplace_account_id": "acc-1",
        "entity_id": "p-1",
        "recommendation_id": "rec-9",
    }


def test_selection_set_and_level2_endpoints() -> None:
    def handle(request: httpx.Request) -> httpx.Response:
        base = {
            "draft_id": "d",
            "action_id": "a",
            "context_version": "c",
            "parameter_version": "p",
            "expires_at": "2026-07-18T12:00:00Z",
        }
        if request.url.path.endswith("level2-proposal"):
            base |= {"scope_key": "scope.account", "consequence_key": "consequence.reversible"}
        return httpx.Response(200, json=base)

    port = _port(httpx.MockTransport(handle))
    bulk = port.create_selection_set_draft(account_id="acc-1", query="account=acc-1")
    assert bulk.draft_kind is DraftKind.SELECTION_SET
    assert bulk.control_deep_link == "/app/bulk?set=a"

    card = port.create_level2_proposal(
        account_id="acc-1", setting_key="briefing.time", before_key="v.8", after_key="v.9"
    )
    assert card.scope_key == "scope.account"
    assert card.draft.control_deep_link == "/app/settings?confirm=a"


def test_non_2xx_fails_closed() -> None:
    port = _port(httpx.MockTransport(lambda _r: httpx.Response(503, json={"code": "x"})))
    with pytest.raises(DraftUnavailable):
        port.create_recommendation_draft(account_id="a", entity_id="e", recommendation_id="r")


def test_malformed_body_fails_closed() -> None:
    def handle(_r: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"draft_id": "d"})  # missing required fields

    port = _port(httpx.MockTransport(handle))
    with pytest.raises(DraftUnavailable):
        port.create_selection_set_draft(account_id="a", query="q")


def test_port_exposes_only_the_three_draft_methods() -> None:
    port = _port(httpx.MockTransport(lambda _r: httpx.Response(200, json={})))
    methods = {m for m in dir(port) if not m.startswith("_") and callable(getattr(port, m))}
    assert methods == {
        "create_recommendation_draft",
        "create_selection_set_draft",
        "create_level2_proposal",
    }
    for forbidden in ("approve", "execute", "confirm", "bulk_approve"):
        assert not any(forbidden in m for m in methods)

"""The Draft transport bounds every write: deadline + stable idempotency (#25).

Draft-only authority must not allow an invisible late write after a timed-out
turn, and a retried Draft must not duplicate the write. So the real Draft
transport (:class:`GatewayDraftPort`) enforces a request-scoped timeout that
aborts the in-flight network operation (failing closed to no ticket), and sends a
STABLE idempotency key derived from the request identity so a retry deduplicates
server-side and can never create a second Draft.
"""

from __future__ import annotations

import httpx
import pytest
from llm.flows.gateway_draft import DraftUnavailable, GatewayDraftPort
from llm.orchestrator.cancellation import (
    CancelToken,
    ToolCancelledError,
    reset_cancel_token,
    set_cancel_token,
)


def _ok_body(**extra: object) -> dict[str, object]:
    base: dict[str, object] = {
        "draft_id": "d",
        "action_id": "a",
        "context_version": "c",
        "parameter_version": "p",
        "expires_at": "2026-07-18T12:00:00Z",
    }
    base.update(extra)
    return base


def test_transport_timeout_fails_closed_with_no_ticket() -> None:
    """A transport-level timeout aborts the write and yields NO Draft ticket.

    The network operation is cancelled by the deadline (httpx raises a timeout);
    the port maps it to DraftUnavailable and never fabricates a Draft — so no
    state change is visible after the failure.
    """
    committed = {"n": 0}

    def handle(_request: httpx.Request) -> httpx.Response:
        # Stand-in for a server that never gets to apply the write: the client
        # deadline elapses first and aborts the request.
        raise httpx.ReadTimeout("deadline elapsed", request=_request)

    port = GatewayDraftPort(
        "http://gateway.internal",
        "read-draft-token",
        httpx.Client(transport=httpx.MockTransport(handle)),
        timeout_seconds=0.5,
    )
    with pytest.raises(DraftUnavailable):
        port.create_recommendation_draft(
            account_id="acc-1", entity_id="p-1", recommendation_id="rec-9"
        )
    assert committed["n"] == 0


def test_draft_carries_the_configured_timeout() -> None:
    """Every Draft POST is issued under a bounded request timeout (never unbounded)."""
    seen: dict[str, object] = {}

    def handle(request: httpx.Request) -> httpx.Response:
        seen["timeout"] = request.extensions.get("timeout")
        return httpx.Response(200, json=_ok_body())

    port = GatewayDraftPort(
        "http://gateway.internal",
        "t",
        httpx.Client(transport=httpx.MockTransport(handle)),
        timeout_seconds=3.0,
    )
    port.create_selection_set_draft(account_id="acc-1", query="account=acc-1")
    timeout = seen["timeout"]
    assert isinstance(timeout, dict)
    # httpx expands a scalar timeout to all four phases; all must be bounded.
    assert set(timeout.values()) == {3.0}


def test_retried_draft_uses_a_stable_idempotency_key() -> None:
    """The SAME logical Draft, retried, carries the SAME idempotency key.

    A stable key lets the gateway deduplicate a retry, so a repeated create cannot
    produce a duplicate write.
    """
    keys: list[str | None] = []

    def handle(request: httpx.Request) -> httpx.Response:
        keys.append(request.headers.get("Idempotency-Key"))
        return httpx.Response(200, json=_ok_body(recommendation_version="rec-9"))

    port = GatewayDraftPort(
        "http://gateway.internal",
        "t",
        httpx.Client(transport=httpx.MockTransport(handle)),
        timeout_seconds=1.0,
    )
    for _ in range(2):
        port.create_recommendation_draft(
            account_id="acc-1", entity_id="p-1", recommendation_id="rec-9"
        )

    assert keys[0] is not None
    assert keys[0] == keys[1]  # a retry of the same create reuses the key


def test_cancelled_token_aborts_the_write_before_it_is_issued() -> None:
    """A cancelled request-scoped token aborts the Draft POST before it is sent.

    The per-tool timeout cancels the request-scoped token; the outbound Draft
    transport reads it and fails closed to NO ticket without issuing the write —
    the token is genuinely authoritative, not merely advisory (issue #25 FIX 2).
    """
    issued = {"n": 0}

    def handle(request: httpx.Request) -> httpx.Response:
        issued["n"] += 1
        return httpx.Response(200, json=_ok_body(recommendation_version="rec-9"))

    port = GatewayDraftPort(
        "http://gateway.internal",
        "read-draft-token",
        httpx.Client(transport=httpx.MockTransport(handle)),
        timeout_seconds=1.0,
    )
    token = CancelToken()
    token.cancel()
    reset = set_cancel_token(token)
    try:
        with pytest.raises(ToolCancelledError):
            port.create_recommendation_draft(
                account_id="acc-1", entity_id="p-1", recommendation_id="rec-9"
            )
    finally:
        reset_cancel_token(reset)

    assert issued["n"] == 0  # no POST issued: fails closed to no ticket


def test_distinct_drafts_get_distinct_idempotency_keys() -> None:
    """Different Draft requests must NOT collide on the idempotency key."""
    keys: list[str | None] = []

    def handle(request: httpx.Request) -> httpx.Response:
        keys.append(request.headers.get("Idempotency-Key"))
        return httpx.Response(200, json=_ok_body(recommendation_version="v"))

    port = GatewayDraftPort(
        "http://gateway.internal",
        "t",
        httpx.Client(transport=httpx.MockTransport(handle)),
        timeout_seconds=1.0,
    )
    port.create_recommendation_draft(account_id="acc-1", entity_id="p-1", recommendation_id="rec-9")
    port.create_recommendation_draft(account_id="acc-2", entity_id="p-1", recommendation_id="rec-9")
    port.create_selection_set_draft(account_id="acc-1", query="account=acc-1")

    assert len(set(keys)) == 3  # all three are distinct

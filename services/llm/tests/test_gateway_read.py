"""The authoritative READ port fails closed and never fabricates (issue #108, 108c).

Unit-level guards on the transport and its fail-closed stub, complementing the
cross-boundary suite in ``test_flow_dispatch_chat.py``. Negative tests first:
"unwired never looks like nothing-to-report" and "money never becomes a float"
are never-cut invariants (§4.6, §9.1).
"""

from __future__ import annotations

import ast
import inspect
from pathlib import Path

import httpx
import pytest
from llm.flows import flow_dispatch, gateway_read, read_ports
from llm.flows.gateway_read import GatewayReadPort
from llm.flows.read_ports import (
    NoReadPort,
    ReadPort,
    ReadUnavailable,
    read_port_methods,
)
from llm.orchestrator.cancellation import CancelToken, ToolCancelledError, set_cancel_token
from llm.orchestrator.scope import TurnScope
from llm.tools import runners

SCOPE = TurnScope(
    organization_id="org-1", marketplace_account_id="acct-1", entity_id="var-1"
)
BASE_URL = "http://core.internal"

# The structural prohibitions §12.3 forbids this plane from HOLDING at all. The
# read port must expose no method that could move state.
FORBIDDEN_VERBS = (
    "approve",
    "execute",
    "confirm",
    "commit",
    "publish",
    "guardrail_write",
    "permission",
    "grant",
    "override",
    "authorize",
    "create",
    "update",
    "delete",
    "draft",
)


def _port(handler: object, *, timeout: float = 5.0) -> GatewayReadPort:
    transport = httpx.MockTransport(handler)  # type: ignore[arg-type]
    return GatewayReadPort(
        BASE_URL, "outbound-token", httpx.Client(transport=transport), timeout_seconds=timeout
    )


# --- the port is structurally read-only --------------------------------------


def test_the_read_port_exposes_no_state_changing_method() -> None:
    """A read can never move state — and there is no method here that could.

    Mirrors the registry containment test one layer down: the structural
    prohibition is the ABSENCE of the capability, so it is asserted over the
    Protocol's own surface, not over a runtime check that could be bypassed.
    """
    methods = {
        name
        for name, _ in inspect.getmembers(ReadPort, predicate=inspect.isfunction)
        if not name.startswith("_")
    }
    assert methods == set(read_port_methods())
    for name in methods:
        lowered = name.lower()
        for verb in FORBIDDEN_VERBS:
            assert verb not in lowered, f"read port method {name!r} looks state-changing"


# --- the fail-closed stub -----------------------------------------------------


@pytest.mark.parametrize("method", sorted(read_port_methods()))
def test_no_read_port_raises_rather_than_returning_an_empty_success(method: str) -> None:
    """"Not wired" must never be indistinguishable from "nothing to report".

    An empty-but-successful read would let a turn answer confidently from no
    evidence. The stub raises on EVERY method instead (§12.4).
    """
    port = NoReadPort()
    kwargs = {
        "briefing": {"business_day": "2026-07-25"},
        "margin_readiness": {"variant_id": "var-1"},
        "recommendation_detail": {"recommendation_id": "rec-1"},
        "actions": {},
        "guardrails": {},
    }[method]
    with pytest.raises(ReadUnavailable):
        getattr(port, method)(SCOPE, **kwargs)


# --- fail-closed transport ----------------------------------------------------


@pytest.mark.parametrize("status", [400, 401, 403, 404, 429, 500, 503])
def test_a_non_2xx_response_fails_closed(status: int) -> None:
    port = _port(lambda _r: httpx.Response(status, json={"error": "x"}))
    with pytest.raises(ReadUnavailable):
        port.actions(SCOPE)


def test_a_malformed_body_fails_closed() -> None:
    port = _port(lambda _r: httpx.Response(200, content=b"<html>nope"))
    with pytest.raises(ReadUnavailable):
        port.actions(SCOPE)


def test_a_non_object_body_fails_closed() -> None:
    port = _port(lambda _r: httpx.Response(200, json=[1, 2, 3]))
    with pytest.raises(ReadUnavailable):
        port.actions(SCOPE)


def test_a_missing_required_field_fails_closed() -> None:
    """A short payload is a lost fact, not a droppable field."""
    port = _port(
        lambda _r: httpx.Response(
            200, json={"items": [{"id": "act-1"}], "hasMore": False}
        )
    )
    with pytest.raises(ReadUnavailable):
        port.actions(SCOPE)


def test_a_transport_error_fails_closed() -> None:
    def explode(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("unreachable")

    with pytest.raises(ReadUnavailable):
        _port(explode).actions(SCOPE)


def test_a_cancelled_tool_call_aborts_before_the_request_is_sent() -> None:
    """The per-tool deadline is authoritative, not advisory (issue #25).

    A read whose cancel token already fired must never leave the process — the
    token is checked BEFORE the request, exactly as the Draft transport does.
    """
    sent: list[str] = []

    def record(request: httpx.Request) -> httpx.Response:
        sent.append(request.url.path)
        return httpx.Response(200, json={"items": []})

    port = _port(record)
    token = CancelToken()
    token.cancel()
    set_cancel_token(token)
    try:
        with pytest.raises(ToolCancelledError):
            port.actions(SCOPE)
    finally:
        set_cancel_token(CancelToken())
    assert sent == []


# --- money never becomes a float ---------------------------------------------


def test_a_float_mantissa_fails_the_read_closed() -> None:
    """Quarantine over inference: an ambiguous amount is never coerced (§9.1)."""
    body = {
        "items": [
            {
                "id": "act-1",
                "state": "accepted",
                "price": {"mantissa": 12345.6, "currency": "IRR", "exponent": 0},
            }
        ]
    }
    with pytest.raises(ReadUnavailable):
        _port(lambda _r: httpx.Response(200, json=body)).actions(SCOPE)


def test_a_non_decimal_mantissa_string_fails_the_read_closed() -> None:
    body = {
        "items": [
            {
                "id": "act-1",
                "state": "accepted",
                "price": {"mantissa": "1,234", "currency": "IRR", "exponent": 0},
            }
        ]
    }
    with pytest.raises(ReadUnavailable):
        _port(lambda _r: httpx.Response(200, json=body)).actions(SCOPE)


def test_an_int64_mantissa_survives_exactly() -> None:
    """Above 2^53 — a lossy float/JS-number round trip would corrupt it (#73)."""
    big = "9007199254740993"
    body = {
        "items": [
            {
                "id": "act-1",
                "state": "accepted",
                "price": {"mantissa": big, "currency": "IRR", "exponent": 0},
            }
        ]
    }
    read = _port(lambda _r: httpx.Response(200, json=body)).actions(SCOPE)
    assert read.actions[0].price is not None
    assert read.actions[0].price.model_dump(mode="json")["mantissa"] == big


def test_no_float_conversion_appears_on_the_read_path() -> None:
    """Static guard: no ``float(...)`` CALL exists in the read transport or models.

    The Go core bans raw float arithmetic on money paths with forbidigo/semgrep;
    this is the same guard on the Python side of the same never-cut invariant
    (§9.1). It walks the AST rather than the raw text, so it detects a real
    conversion and is not satisfied — or tripped — by prose in a docstring.
    """
    for module in (gateway_read, read_ports, runners, flow_dispatch):
        tree = ast.parse(Path(inspect.getfile(module)).read_text(encoding="utf-8"))
        calls = [
            node
            for node in ast.walk(tree)
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Name)
            and node.func.id == "float"
        ]
        assert not calls, f"a float conversion appeared in {module.__name__}"

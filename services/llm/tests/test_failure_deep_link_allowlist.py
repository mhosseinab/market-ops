"""Failure/refusal deep links are constrained to a closed recovery-route set.

Issue #56: a structured failure (`TurnFailure`, §12.4) or a cannot-answer refusal
(`CannotAnswer`, CHAT-005) must not accept a model-provided / free-form deep link.
An external scheme, protocol-relative URL, host, path traversal, encoded bypass,
or unknown internal path must fail closed; only an approved internal recovery
route passes. These are negative tests first (never-cut §4.6 free-text
containment / screens-only fallback).
"""

from __future__ import annotations

import pytest
from llm.envelope.composer import FALLBACK_DEEP_LINK, fail_closed
from llm.envelope.contract import CannotAnswer
from llm.envelope.models import TurnFailure
from llm.flows.deep_links import (
    RECOVERY_ROUTES,
    ROUTE_TODAY,
    SCREENS_FALLBACK,
    validate_recovery_route,
)
from llm.orchestrator.graph import _DEEP_LINK, _turn_failure
from pydantic import ValidationError

# Values that MUST fail closed — an attacker-controlled or model-authored link.
_UNSAFE_DEEP_LINKS = [
    "https://evil.example.com",
    "http://evil.example.com/app/today",
    "javascript:alert(1)",
    "//evil.example.com",
    "/app/../admin",
    "/app/today/../../etc/passwd",
    "%2e%2e%2fadmin",
    "/app/today%2f..%2fadmin",
    "/app/screens",  # unknown internal path (not in the design route map)
    "/admin",
    "app/today",  # missing leading slash — not the canonical route
    "/app/today?next=https://evil.example.com",  # query smuggling
    " /app/today",  # leading whitespace bypass attempt
    "",
]


# --- the shared validator ----------------------------------------------------


@pytest.mark.parametrize("route", sorted(RECOVERY_ROUTES))
def test_validate_recovery_route_accepts_approved_routes(route: str) -> None:
    assert validate_recovery_route(route) == route


@pytest.mark.parametrize("bad", _UNSAFE_DEEP_LINKS)
def test_validate_recovery_route_rejects_unsafe(bad: str) -> None:
    with pytest.raises(ValueError):
        validate_recovery_route(bad)


def test_recovery_route_error_does_not_echo_rejected_value() -> None:
    secret = "https://phish.example.com/steal"
    with pytest.raises(ValueError) as exc:
        validate_recovery_route(secret)
    assert secret not in str(exc.value)  # free-text containment (§8)


# --- TurnFailure (§12.4 structured failure) ----------------------------------


@pytest.mark.parametrize("route", sorted(RECOVERY_ROUTES))
def test_turn_failure_accepts_recovery_route(route: str) -> None:
    tf = TurnFailure(code="X", message="m", deep_link=route)
    assert tf.deep_link == route


def test_turn_failure_allows_none_deep_link() -> None:
    assert TurnFailure(code="X", message="m").deep_link is None


@pytest.mark.parametrize("bad", _UNSAFE_DEEP_LINKS)
def test_turn_failure_rejects_unsafe_deep_link(bad: str) -> None:
    with pytest.raises(ValidationError):
        TurnFailure(code="X", message="m", deep_link=bad)


# --- CannotAnswer (CHAT-005 refusal) -----------------------------------------


@pytest.mark.parametrize("route", sorted(RECOVERY_ROUTES))
def test_cannot_answer_accepts_recovery_route(route: str) -> None:
    ca = CannotAnswer(reason_key="state.degraded.body", message="m", deep_link=route)
    assert ca.deep_link == route


@pytest.mark.parametrize("bad", _UNSAFE_DEEP_LINKS)
def test_cannot_answer_rejects_unsafe_deep_link(bad: str) -> None:
    with pytest.raises(ValidationError):
        CannotAnswer(reason_key="state.degraded.body", message="m", deep_link=bad)


# --- the deterministic producers only ever emit approved routes --------------


def test_graph_turn_failure_constant_is_a_recovery_route() -> None:
    assert _DEEP_LINK in RECOVERY_ROUTES
    assert _turn_failure("ERR", "boom").deep_link in RECOVERY_ROUTES


def test_composer_fallback_is_a_recovery_route() -> None:
    assert FALLBACK_DEEP_LINK in RECOVERY_ROUTES
    assert FALLBACK_DEEP_LINK == SCREENS_FALLBACK == ROUTE_TODAY


def test_fail_closed_helper_emits_recovery_route() -> None:
    assert fail_closed(message="cannot answer").deep_link in RECOVERY_ROUTES

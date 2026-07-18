"""Blocker guidance determinism (CHAT-070/071/072).

CHAT-070: chat lists blockers in policy order and the order/reason codes
byte-match the engine sources (internal/policy Stage; internal/cost
AllComponents). CHAT-071: one blocker at a time, single-value cost control.
CHAT-072: refresh consumes a bounded route budget.
"""

from __future__ import annotations

from llm.flows.blockers import (
    COST_COMPONENT_ORDER,
    POLICY_STAGE_ORDER,
    Blocker,
    RefreshBudget,
    next_blocker,
    order_cost_blockers,
    order_policy_blockers,
)
from llm.flows.models import CostControl


def test_policy_stage_order_byte_matches_engine() -> None:
    """The stage order equals internal/policy's Stage order (§9.3) byte-for-byte."""
    assert POLICY_STAGE_ORDER == (
        "boundary",
        "hard_floor",
        "movement_cap",
        "cooldown",
        "strategy",
        "objective",
    )


def test_cost_component_order_byte_matches_engine() -> None:
    """The component order equals internal/cost AllComponents (§9.2) byte-for-byte."""
    assert COST_COMPONENT_ORDER == (
        "cogs",
        "commission",
        "fulfillment",
        "shipping",
        "packaging",
        "promotion",
        "ads",
        "returns",
    )


def test_policy_blockers_order_to_policy_order() -> None:
    shuffled = [
        Blocker(code="cooldown_active", reason_key="r.cd", affected_count=3),
        Blocker(code="boundary_unknown", reason_key="r.bu", affected_count=1),
        Blocker(code="contribution_below_floor", reason_key="r.bf", affected_count=7),
    ]
    ordered = order_policy_blockers(shuffled)
    assert [b.code for b in ordered] == [
        "boundary_unknown",
        "contribution_below_floor",
        "cooldown_active",
    ]


def test_cost_blockers_order_to_component_order() -> None:
    shuffled = [
        Blocker(code="shipping", reason_key="r.sh", affected_count=2),
        Blocker(code="cogs", reason_key="r.cogs", affected_count=5),
        Blocker(code="commission", reason_key="r.com", affected_count=4),
    ]
    ordered = order_cost_blockers(shuffled)
    assert [b.code for b in ordered] == ["cogs", "commission", "shipping"]


def test_one_blocker_at_a_time() -> None:
    ordered = [
        Blocker(code="cogs", reason_key="r.cogs", affected_count=5, resolved=True),
        Blocker(
            code="commission",
            reason_key="r.com",
            affected_count=4,
            cost_control=CostControl(field_key="cost.commission"),
        ),
        Blocker(code="shipping", reason_key="r.sh", affected_count=2),
    ]
    nb = next_blocker(ordered)
    assert nb is not None and nb.code == "commission"
    # The single-value cost control is present for the current blocker (CHAT-071).
    assert nb.cost_control is not None and nb.cost_control.inline is True


def test_all_resolved_yields_no_next_blocker() -> None:
    ordered = [Blocker(code="cogs", reason_key="r", affected_count=1, resolved=True)]
    assert next_blocker(ordered) is None


def test_refresh_budget_is_bounded() -> None:
    budget = RefreshBudget(remaining=2)
    assert budget.consume() is True
    assert budget.consume() is True
    # Exhausted: further refreshes are denied (degrade to deep link, §17.3).
    assert budget.consume() is False
    assert budget.remaining == 0

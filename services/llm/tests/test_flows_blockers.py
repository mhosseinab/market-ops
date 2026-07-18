"""Blocker guidance determinism (CHAT-070/071/072).

CHAT-070: chat lists blockers in policy order and the order/reason codes
byte-match the engine sources (internal/policy Stage; internal/cost
AllComponents). CHAT-071: one blocker at a time, single-value cost control.
CHAT-072: refresh consumes a bounded route budget.
"""

from __future__ import annotations

import json
from pathlib import Path

from llm.flows.blockers import (
    COST_COMPONENT_ORDER,
    POLICY_BLOCKER_STAGE,
    POLICY_STAGE_ORDER,
    Blocker,
    RefreshBudget,
    next_blocker,
    order_cost_blockers,
    order_policy_blockers,
)
from llm.flows.models import CostControl

# The cross-language golden emitted from the Go engine constants
# (services/core/internal/policy/order_golden_test.go). Consuming it here means a
# reorder of the Go Stage iota / cost.AllComponents regenerates the golden and
# forces THIS Python test red — not a self-referential Python literal (CHAT-070).
_GOLDEN = json.loads(
    (Path(__file__).resolve().parents[3] / "contracts" / "fixtures" / "blocker_order.json")
    .read_text(encoding="utf-8")
)


def test_policy_stage_order_matches_cross_language_golden() -> None:
    """Chat's stage order equals the Go engine golden (§9.3), not a Python literal."""
    assert POLICY_STAGE_ORDER == tuple(_GOLDEN["policy_stage_order"])


def test_policy_blocker_stage_matches_cross_language_golden() -> None:
    """Chat's blocker→stage map equals the Go engine golden (CHAT-070)."""
    assert POLICY_BLOCKER_STAGE == _GOLDEN["policy_blocker_stage"]


def test_cost_component_order_matches_cross_language_golden() -> None:
    """Chat's component order equals the Go cost golden (§9.2)."""
    assert COST_COMPONENT_ORDER == tuple(_GOLDEN["cost_component_order"])


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

"""Blocker guidance (Journey 9, PRD §6.10, CHAT-070/071/072). Deterministic.

Chat lists blockers in *policy order* with affected counts, handles ONE blocker
at a time, offers a structured single-value cost control (S12) for a single-value
fix and a deep link for complex diagnosis (CSV import), and refreshes
executability after every completed step — consuming a route budget each refresh
(CHAT-072). The order and the reason codes byte-match the engines that own them
(CHAT-070): this module mirrors the Go single sources rather than inventing an
order.

Two blocker domains, each mirroring its Go source of truth:

* **policy stages** — ``internal/policy`` orders blockers by ``Stage`` (§9.3):
  boundary → hard_floor → movement_cap → cooldown → strategy → objective;
* **cost-readiness components** — ``internal/cost`` orders missing/stale
  components by ``AllComponents`` (§9.2): cogs → commission → fulfillment →
  shipping → packaging → promotion → ads → returns.

If the order here drifts from the Go constants, the byte-match test fails — this
module is a mirror, not a second opinion.
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field

from llm.flows.models import CostControl

# --- policy-stage order (mirrors internal/policy Stage, §9.3) -----------------
# The tuple order IS the policy precedence order (lower index = earlier stage).
# Each pair is (stage token, blocker codes admissible at that stage), mirroring
# BlockerCode ↔ Stage in evaluate.go. Byte-matched by test_blocker_order.
POLICY_STAGE_ORDER: tuple[str, ...] = (
    "boundary",
    "hard_floor",
    "movement_cap",
    "cooldown",
    "strategy",
    "objective",
)

POLICY_BLOCKER_STAGE: dict[str, str] = {
    "boundary_unknown": "boundary",
    "boundary_invalid": "boundary",
    "contribution_below_floor": "hard_floor",
    "contribution_crosses_zero": "hard_floor",
    "movement_cap_infeasible": "movement_cap",
    "cooldown_active": "cooldown",
    "strategy_disabled": "strategy",
    "objective_infeasible": "objective",
}

# --- cost-readiness component order (mirrors internal/cost AllComponents, §9.2)
COST_COMPONENT_ORDER: tuple[str, ...] = (
    "cogs",
    "commission",
    "fulfillment",
    "shipping",
    "packaging",
    "promotion",
    "ads",
    "returns",
)


class Blocker(BaseModel):
    """One typed blocker surfaced to chat. ``code`` is the machine reason; the
    surface localizes ``reason_key``. ``affected_count`` is the number of listings
    the blocker affects (CHAT-070). No blocker ever carries an approval control.
    """

    model_config = ConfigDict(extra="forbid")

    code: str
    reason_key: str  # canonical catalog key for the reason text
    affected_count: int
    resolved: bool = False
    # A single-value cost fix, when the blocker is a missing/stale cost component.
    cost_control: CostControl | None = None


def order_policy_blockers(blockers: list[Blocker]) -> list[Blocker]:
    """Order policy blockers in policy-stage order (CHAT-070). Stable within a
    stage, so equal-stage blockers keep their engine order.
    """
    return sorted(
        blockers,
        key=lambda b: POLICY_STAGE_ORDER.index(POLICY_BLOCKER_STAGE[b.code]),
    )


def order_cost_blockers(blockers: list[Blocker]) -> list[Blocker]:
    """Order cost-readiness component blockers in AllComponents order (CHAT-070)."""
    return sorted(blockers, key=lambda b: COST_COMPONENT_ORDER.index(b.code))


def next_blocker(ordered: list[Blocker]) -> Blocker | None:
    """The single next unresolved blocker to handle (one-at-a-time, CHAT-071).

    Returns the first not-yet-resolved blocker in the given (already ordered)
    list, or ``None`` when every blocker is resolved and executability can be
    re-derived.
    """
    for blocker in ordered:
        if not blocker.resolved:
            return blocker
    return None


class RefreshBudget(BaseModel):
    """A bounded budget for executability refreshes (CHAT-072).

    Each completed blocker step re-derives executability via a route read, which
    costs one unit. When the budget is exhausted the flow degrades to a deep link
    to the structured screen rather than looping refreshes unbounded (§17.3).
    """

    model_config = ConfigDict(extra="forbid")

    remaining: int = Field(ge=0)

    def consume(self) -> bool:
        """Consume one refresh unit. Returns True if a refresh was allowed."""
        if self.remaining <= 0:
            return False
        self.remaining -= 1
        return True

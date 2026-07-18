"""Execution monitoring (Journey 10, PRD §6.11, CHAT-073/074). Deterministic.

Chat groups in-flight and completed actions by their terminal state, reading the
append-only action state history (never mutating it). Retry is BLOCKED while an
action is unreconciled (CHAT-074) — the same EXE-003 rule the execution engine
enforces; chat cannot bypass it. A retry request for a still-reconciling action
is answered with a deep link to the structured screen, never a re-execution
(there is no execute path in the model plane at all).
"""

from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field


class ActionState(StrEnum):
    """Action lifecycle states (canonical glossary keys map 1:1, §11.4).

    Terminal states are accepted / rejected / failed / expired.
    ``pending_reconciliation`` is NOT terminal: the marketplace outcome is not yet
    reconciled, so a retry is blocked (EXE-003 / CHAT-074).
    """

    AWAITING_CONFIRMATION = "awaiting_confirmation"
    EXECUTING = "executing"
    PENDING_RECONCILIATION = "pending_reconciliation"
    ACCEPTED = "accepted"
    REJECTED = "rejected"
    FAILED = "failed"
    EXPIRED = "expired"


TERMINAL_STATES: frozenset[ActionState] = frozenset(
    {
        ActionState.ACCEPTED,
        ActionState.REJECTED,
        ActionState.FAILED,
        ActionState.EXPIRED,
    }
)

# States from which a retry is BLOCKED because the outcome is not yet reconciled
# (CHAT-074, mirrors EXE-003). An action mid-flight or awaiting reconciliation
# must never be retried; only a terminal *failure/rejection* is retry-eligible.
UNRECONCILED_STATES: frozenset[ActionState] = frozenset(
    {
        ActionState.AWAITING_CONFIRMATION,
        ActionState.EXECUTING,
        ActionState.PENDING_RECONCILIATION,
    }
)

# Canonical state key per action state (CHAT-022 glossary keys).
_STATE_KEY: dict[ActionState, str] = {
    ActionState.AWAITING_CONFIRMATION: "state.awaitingConfirmation",
    ActionState.EXECUTING: "state.executing",
    ActionState.PENDING_RECONCILIATION: "state.pendingReconciliation",
    ActionState.ACCEPTED: "state.accepted",
    ActionState.REJECTED: "state.rejected",
    ActionState.FAILED: "state.failed",
    ActionState.EXPIRED: "state.expired",
}


class ActionRecord(BaseModel):
    """A read-only projection of the append-only action state history."""

    model_config = ConfigDict(extra="forbid")

    action_id: str
    state: ActionState
    entity_id: str | None = None

    def state_key(self) -> str:
        return _STATE_KEY[self.state]


class MonitoringView(BaseModel):
    """Actions grouped by state for the monitoring surface (CHAT-073)."""

    model_config = ConfigDict(extra="forbid")

    groups: dict[str, list[str]] = Field(default_factory=dict)

    def action_ids_in(self, state: ActionState) -> list[str]:
        return self.groups.get(state.value, [])


def group_by_state(actions: list[ActionRecord]) -> MonitoringView:
    """Group action ids by their current state (CHAT-073). Deterministic order.

    Within a group, action ids preserve the order they arrived from the
    append-only history — the surface never re-sorts across the state boundary.
    """
    groups: dict[str, list[str]] = {}
    for record in actions:
        groups.setdefault(record.state.value, []).append(record.action_id)
    return MonitoringView(groups=groups)


def can_retry(record: ActionRecord) -> bool:
    """Whether a retry may be *offered* for an action (CHAT-074 / EXE-003).

    Retry is blocked while the action is unreconciled (mid-flight or awaiting
    reconciliation). Only a terminal failure/rejection/expiry is retry-eligible;
    an accepted action needs no retry. This gates whether chat SHOWS a retry
    deep link — chat never executes a retry itself (no execute path exists).
    """
    if record.state in UNRECONCILED_STATES:
        return False
    return record.state in {ActionState.FAILED, ActionState.REJECTED, ActionState.EXPIRED}

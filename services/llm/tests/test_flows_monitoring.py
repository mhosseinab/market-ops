"""Execution monitoring determinism (CHAT-073/074).

CHAT-073: actions group by terminal state from the append-only history.
CHAT-074: retry is blocked while an action is unreconciled (mirrors EXE-003) —
chat cannot bypass it.
"""

from __future__ import annotations

from llm.flows.monitoring import (
    ActionRecord,
    ActionState,
    can_retry,
    group_by_state,
)


def _records() -> list[ActionRecord]:
    return [
        ActionRecord(action_id="a-1", state=ActionState.ACCEPTED),
        ActionRecord(action_id="a-2", state=ActionState.PENDING_RECONCILIATION),
        ActionRecord(action_id="a-3", state=ActionState.FAILED),
        ActionRecord(action_id="a-4", state=ActionState.ACCEPTED),
        ActionRecord(action_id="a-5", state=ActionState.EXECUTING),
    ]


def test_group_by_state() -> None:
    view = group_by_state(_records())
    assert view.action_ids_in(ActionState.ACCEPTED) == ["a-1", "a-4"]
    assert view.action_ids_in(ActionState.FAILED) == ["a-3"]
    assert view.action_ids_in(ActionState.PENDING_RECONCILIATION) == ["a-2"]


def test_retry_blocked_while_unreconciled() -> None:
    def retry(state: ActionState) -> bool:
        return can_retry(ActionRecord(action_id="a", state=state))

    # CHAT-074: pending reconciliation / mid-flight ⇒ retry blocked.
    assert retry(ActionState.PENDING_RECONCILIATION) is False
    assert retry(ActionState.EXECUTING) is False
    assert retry(ActionState.AWAITING_CONFIRMATION) is False


def test_retry_allowed_only_for_terminal_failure() -> None:
    assert can_retry(ActionRecord(action_id="a-3", state=ActionState.FAILED)) is True
    assert can_retry(ActionRecord(action_id="a-7", state=ActionState.REJECTED)) is True
    assert can_retry(ActionRecord(action_id="a-8", state=ActionState.EXPIRED)) is True
    # An accepted action needs no retry.
    assert can_retry(ActionRecord(action_id="a-1", state=ActionState.ACCEPTED)) is False

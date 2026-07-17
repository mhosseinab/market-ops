from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.approval_state import ApprovalState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApprovalStateHistoryEntry")


@_attrs_define
class ApprovalStateHistoryEntry:
    """One append-only §8.4 state-history row (AUD-001).

    Attributes:
        to_state (ApprovalState): One node of the §8.4 approval state machine. The set is closed; it is the
            authoritative lifecycle vocabulary for a card and its history.
        reason (str): Invalidation dimension or transition note (never authority).
        occurred_at (datetime.datetime):
        from_state (ApprovalState | Unset): One node of the §8.4 approval state machine. The set is closed; it is the
            authoritative lifecycle vocabulary for a card and its history.
    """

    to_state: ApprovalState
    reason: str
    occurred_at: datetime.datetime
    from_state: ApprovalState | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        to_state = self.to_state.value

        reason = self.reason

        occurred_at = self.occurred_at.isoformat()

        from_state: str | Unset = UNSET
        if not isinstance(self.from_state, Unset):
            from_state = self.from_state.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "toState": to_state,
                "reason": reason,
                "occurredAt": occurred_at,
            }
        )
        if from_state is not UNSET:
            field_dict["fromState"] = from_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        to_state = ApprovalState(d.pop("toState"))

        reason = d.pop("reason")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurredAt"))

        _from_state = d.pop("fromState", UNSET)
        from_state: ApprovalState | Unset
        if isinstance(_from_state, Unset):
            from_state = UNSET
        else:
            from_state = ApprovalState(_from_state)

        approval_state_history_entry = cls(
            to_state=to_state,
            reason=reason,
            occurred_at=occurred_at,
            from_state=from_state,
        )

        return approval_state_history_entry

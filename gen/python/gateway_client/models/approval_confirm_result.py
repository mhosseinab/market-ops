from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.approval_invalidation_reason import ApprovalInvalidationReason
from ..models.approval_state import ApprovalState

T = TypeVar("T", bound="ApprovalConfirmResult")


@_attrs_define
class ApprovalConfirmResult:
    """The outcome of activating a structured control (§8.4). `state` is one of approved, invalidated, or expired. When
    invalidated, `reason` names the changed dimension (APR-001). `executionPending` is true when the card reached
    Approved: per PRD the Revalidating → Executing boundary lands in S18, so no write occurs here.

        Attributes:
            card_id (UUID):
            state (ApprovalState): One node of the §8.4 approval state machine. The set is closed; it is the authoritative
                lifecycle vocabulary for a card and its history.
            reason (ApprovalInvalidationReason): The exact bound dimension that invalidated an approval control (APR-001,
                §16). Empty means the control is still valid.
            execution_pending (bool): True when Approved; execution/reconciliation is S18.
    """

    card_id: UUID
    state: ApprovalState
    reason: ApprovalInvalidationReason
    execution_pending: bool

    def to_dict(self) -> dict[str, Any]:
        card_id = str(self.card_id)

        state = self.state.value

        reason = self.reason.value

        execution_pending = self.execution_pending

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cardId": card_id,
                "state": state,
                "reason": reason,
                "executionPending": execution_pending,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        card_id = UUID(d.pop("cardId"))

        state = ApprovalState(d.pop("state"))

        reason = ApprovalInvalidationReason(d.pop("reason"))

        execution_pending = d.pop("executionPending")

        approval_confirm_result = cls(
            card_id=card_id,
            state=state,
            reason=reason,
            execution_pending=execution_pending,
        )

        return approval_confirm_result

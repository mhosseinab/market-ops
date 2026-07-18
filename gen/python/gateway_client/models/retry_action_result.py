from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.execution_external_state import ExecutionExternalState
from ..types import UNSET, Unset

T = TypeVar("T", bound="RetryActionResult")


@_attrs_define
class RetryActionResult:
    """The retry eligibility outcome. `eligible` is true only for a definitively Failed action; a Pending Reconciliation
    action is refused with an error.

        Attributes:
            action_id (UUID):
            eligible (bool):
            state (ExecutionExternalState | Unset): The EXE-003 external result of a write. `pending_reconciliation` is the
                fail-closed state for an UNKNOWN result — never inferred as success/failure.
    """

    action_id: UUID
    eligible: bool
    state: ExecutionExternalState | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        eligible = self.eligible

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "eligible": eligible,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        eligible = d.pop("eligible")

        _state = d.pop("state", UNSET)
        state: ExecutionExternalState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = ExecutionExternalState(_state)

        retry_action_result = cls(
            action_id=action_id,
            eligible=eligible,
            state=state,
        )

        return retry_action_result

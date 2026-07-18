from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.execution_external_state import ExecutionExternalState
from ..models.execution_mode import ExecutionMode
from ..types import UNSET, Unset

T = TypeVar("T", bound="ActionExecutionView")


@_attrs_define
class ActionExecutionView:
    """The single EXE-002 execution record for an action (CHAT-073 read).

    Attributes:
        action_id (UUID):
        card_id (UUID):
        mode (ExecutionMode): The execution mode of a completed Execute call. `write` attempted a real external write
            (write enabled); `recommend_only` tracked the approved action for external matching because writes are OFF
            (EXE-005).
        external_state (ExecutionExternalState): The EXE-003 external result of a write. `pending_reconciliation` is the
            fail-closed state for an UNKNOWN result — never inferred as success/failure.
        external_ref (str | Unset): The marketplace's handle for the write (e.g. batch id), when present.
        reconciled_at (datetime.datetime | Unset): When the external result was reconciled to a terminal state.
    """

    action_id: UUID
    card_id: UUID
    mode: ExecutionMode
    external_state: ExecutionExternalState
    external_ref: str | Unset = UNSET
    reconciled_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        card_id = str(self.card_id)

        mode = self.mode.value

        external_state = self.external_state.value

        external_ref = self.external_ref

        reconciled_at: str | Unset = UNSET
        if not isinstance(self.reconciled_at, Unset):
            reconciled_at = self.reconciled_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "cardId": card_id,
                "mode": mode,
                "externalState": external_state,
            }
        )
        if external_ref is not UNSET:
            field_dict["externalRef"] = external_ref
        if reconciled_at is not UNSET:
            field_dict["reconciledAt"] = reconciled_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        card_id = UUID(d.pop("cardId"))

        mode = ExecutionMode(d.pop("mode"))

        external_state = ExecutionExternalState(d.pop("externalState"))

        external_ref = d.pop("externalRef", UNSET)

        _reconciled_at = d.pop("reconciledAt", UNSET)
        reconciled_at: datetime.datetime | Unset
        if isinstance(_reconciled_at, Unset):
            reconciled_at = UNSET
        else:
            reconciled_at = datetime.datetime.fromisoformat(_reconciled_at)

        action_execution_view = cls(
            action_id=action_id,
            card_id=card_id,
            mode=mode,
            external_state=external_state,
            external_ref=external_ref,
            reconciled_at=reconciled_at,
        )

        return action_execution_view

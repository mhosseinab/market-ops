from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.execution_external_state import ExecutionExternalState
from ..models.execution_gate import ExecutionGate
from ..models.execution_mode import ExecutionMode
from ..models.recommend_only_state import RecommendOnlyState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecuteActionResult")


@_attrs_define
class ExecuteActionResult:
    """The outcome of an Execute call. `blocked` is true when an EXE-001 gate prevented the write (`failedGate` names it).
    For a write, `externalState` is the EXE-003 result and `didWrite` reports whether THIS call performed the single
    external write. For recommend-only, `recommendOnlyState` is set.

        Attributes:
            action_id (UUID):
            card_id (UUID):
            mode (ExecutionMode): The execution mode of a completed Execute call. `write` attempted a real external write
                (write enabled); `recommend_only` tracked the approved action for external matching because writes are OFF
                (EXE-005).
            blocked (bool):
            did_write (bool): True only when THIS call performed the single external write (EXE-002).
            failed_gate (ExecutionGate | Unset): One of the nine EXE-001 revalidation gates. Present on a blocked result to
                name the gate that prevented the write.
            external_state (ExecutionExternalState | Unset): The EXE-003 external result of a write.
                `pending_reconciliation` is the fail-closed state for an UNKNOWN result — never inferred as success/failure.
            recommend_only_state (RecommendOnlyState | Unset): The EXE-005 recommend-only tracking state.
    """

    action_id: UUID
    card_id: UUID
    mode: ExecutionMode
    blocked: bool
    did_write: bool
    failed_gate: ExecutionGate | Unset = UNSET
    external_state: ExecutionExternalState | Unset = UNSET
    recommend_only_state: RecommendOnlyState | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        card_id = str(self.card_id)

        mode = self.mode.value

        blocked = self.blocked

        did_write = self.did_write

        failed_gate: str | Unset = UNSET
        if not isinstance(self.failed_gate, Unset):
            failed_gate = self.failed_gate.value

        external_state: str | Unset = UNSET
        if not isinstance(self.external_state, Unset):
            external_state = self.external_state.value

        recommend_only_state: str | Unset = UNSET
        if not isinstance(self.recommend_only_state, Unset):
            recommend_only_state = self.recommend_only_state.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "cardId": card_id,
                "mode": mode,
                "blocked": blocked,
                "didWrite": did_write,
            }
        )
        if failed_gate is not UNSET:
            field_dict["failedGate"] = failed_gate
        if external_state is not UNSET:
            field_dict["externalState"] = external_state
        if recommend_only_state is not UNSET:
            field_dict["recommendOnlyState"] = recommend_only_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        card_id = UUID(d.pop("cardId"))

        mode = ExecutionMode(d.pop("mode"))

        blocked = d.pop("blocked")

        did_write = d.pop("didWrite")

        _failed_gate = d.pop("failedGate", UNSET)
        failed_gate: ExecutionGate | Unset
        if isinstance(_failed_gate, Unset):
            failed_gate = UNSET
        else:
            failed_gate = ExecutionGate(_failed_gate)

        _external_state = d.pop("externalState", UNSET)
        external_state: ExecutionExternalState | Unset
        if isinstance(_external_state, Unset):
            external_state = UNSET
        else:
            external_state = ExecutionExternalState(_external_state)

        _recommend_only_state = d.pop("recommendOnlyState", UNSET)
        recommend_only_state: RecommendOnlyState | Unset
        if isinstance(_recommend_only_state, Unset):
            recommend_only_state = UNSET
        else:
            recommend_only_state = RecommendOnlyState(_recommend_only_state)

        execute_action_result = cls(
            action_id=action_id,
            card_id=card_id,
            mode=mode,
            blocked=blocked,
            did_write=did_write,
            failed_gate=failed_gate,
            external_state=external_state,
            recommend_only_state=recommend_only_state,
        )

        return execute_action_result

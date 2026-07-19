from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.action_canonical_state import ActionCanonicalState
from ..models.execution_external_state import ExecutionExternalState
from ..models.execution_mode import ExecutionMode
from ..models.recommend_only_state import RecommendOnlyState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ActionExecutionView")


@_attrs_define
class ActionExecutionView:
    """The common action-execution read (CHAT-073), resolving an action in EITHER execution mode (issue #106). For a
    `write` action `externalState` is the single EXE-002/EXE-003 external result. For a `recommend_only` action
    `externalState` is ABSENT (a recommend-only action is not a marketplace write) and `recommendOnlyState` carries its
    EXE-005 state instead. `canonicalState` is the mode-independent lifecycle bucket for grouping.

        Attributes:
            action_id (UUID):
            card_id (UUID):
            mode (ExecutionMode): The execution mode of a completed Execute call. `write` attempted a real external write
                (write enabled); `recommend_only` tracked the approved action for external matching because writes are OFF
                (EXE-005).
            canonical_state (ActionCanonicalState | Unset): The mode-independent lifecycle bucket an action is grouped by in
                the common action API (issue #106). It unifies the write (EXE-003) and recommend-only (EXE-005) result sets so a
                client can group BOTH modes by one stable key, WITHOUT collapsing the authoritative `mode` distinction — a
                recommend-only `externally_executed` action shares the `succeeded` bucket with a marketplace-accepted write, but
                its `mode` stays `recommend_only`, so it is never presented as a marketplace write. `awaiting` = write
                pending_reconciliation or recommend-only awaiting_external_execution; `succeeded` = write accepted or recommend-
                only externally_executed; `rejected`/`failed` = write only; `lapsed` = recommend-only only.
            external_state (ExecutionExternalState | Unset): The EXE-003 external result of a write.
                `pending_reconciliation` is the fail-closed state for an UNKNOWN result — never inferred as success/failure.
            recommend_only_state (RecommendOnlyState | Unset): The EXE-005 recommend-only tracking state.
            external_ref (str | Unset): The marketplace's handle for the write (e.g. batch id), when present.
            reconciled_at (datetime.datetime | Unset): When the external result was reconciled to a terminal state.
    """

    action_id: UUID
    card_id: UUID
    mode: ExecutionMode
    canonical_state: ActionCanonicalState | Unset = UNSET
    external_state: ExecutionExternalState | Unset = UNSET
    recommend_only_state: RecommendOnlyState | Unset = UNSET
    external_ref: str | Unset = UNSET
    reconciled_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        card_id = str(self.card_id)

        mode = self.mode.value

        canonical_state: str | Unset = UNSET
        if not isinstance(self.canonical_state, Unset):
            canonical_state = self.canonical_state.value

        external_state: str | Unset = UNSET
        if not isinstance(self.external_state, Unset):
            external_state = self.external_state.value

        recommend_only_state: str | Unset = UNSET
        if not isinstance(self.recommend_only_state, Unset):
            recommend_only_state = self.recommend_only_state.value

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
            }
        )
        if canonical_state is not UNSET:
            field_dict["canonicalState"] = canonical_state
        if external_state is not UNSET:
            field_dict["externalState"] = external_state
        if recommend_only_state is not UNSET:
            field_dict["recommendOnlyState"] = recommend_only_state
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

        _canonical_state = d.pop("canonicalState", UNSET)
        canonical_state: ActionCanonicalState | Unset
        if isinstance(_canonical_state, Unset):
            canonical_state = UNSET
        else:
            canonical_state = ActionCanonicalState(_canonical_state)

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
            canonical_state=canonical_state,
            external_state=external_state,
            recommend_only_state=recommend_only_state,
            external_ref=external_ref,
            reconciled_at=reconciled_at,
        )

        return action_execution_view

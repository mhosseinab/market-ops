from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.action_canonical_state import ActionCanonicalState
from ..models.approval_state import ApprovalState
from ..models.execution_external_state import ExecutionExternalState
from ..models.execution_mode import ExecutionMode
from ..models.recommend_only_state import RecommendOnlyState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="ActionSummary")


@_attrs_define
class ActionSummary:
    """One row of the actions queue (PD-3 item 5) — an approval card, unexpanded. When the action has been executed or is
    tracked recommend-only, the execution overlay is present so the list can group by canonical state WITHOUT deep-link-
    only discovery (issue #106): `executionMode` names the mode, `canonicalState` the mode-independent lifecycle bucket,
    and exactly one of `externalState` (write) / `recommendOnlyState` (recommend-only) carries the mode-specific raw
    state. All overlay fields are absent for a pre-execution card (still
    Draft/ReadyForReview/AwaitingConfirmation/Approved).

        Attributes:
            id (UUID):
            recommendation_id (UUID):
            version (int):
            state (ApprovalState): One node of the §8.4 approval state machine. The set is closed; it is the authoritative
                lifecycle vocabulary for a card and its history.
            price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
                mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            expires_at (datetime.datetime):
            idempotency_key (str | Unset):
            created_at (datetime.datetime | Unset):
            execution_mode (ExecutionMode | Unset): The execution mode of a completed Execute call. `write` attempted a real
                external write (write enabled); `recommend_only` tracked the approved action for external matching because
                writes are OFF (EXE-005).
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
    """

    id: UUID
    recommendation_id: UUID
    version: int
    state: ApprovalState
    price: MoneyAmount
    expires_at: datetime.datetime
    idempotency_key: str | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    execution_mode: ExecutionMode | Unset = UNSET
    canonical_state: ActionCanonicalState | Unset = UNSET
    external_state: ExecutionExternalState | Unset = UNSET
    recommend_only_state: RecommendOnlyState | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        recommendation_id = str(self.recommendation_id)

        version = self.version

        state = self.state.value

        price = self.price.to_dict()

        expires_at = self.expires_at.isoformat()

        idempotency_key = self.idempotency_key

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        execution_mode: str | Unset = UNSET
        if not isinstance(self.execution_mode, Unset):
            execution_mode = self.execution_mode.value

        canonical_state: str | Unset = UNSET
        if not isinstance(self.canonical_state, Unset):
            canonical_state = self.canonical_state.value

        external_state: str | Unset = UNSET
        if not isinstance(self.external_state, Unset):
            external_state = self.external_state.value

        recommend_only_state: str | Unset = UNSET
        if not isinstance(self.recommend_only_state, Unset):
            recommend_only_state = self.recommend_only_state.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "recommendationId": recommendation_id,
                "version": version,
                "state": state,
                "price": price,
                "expiresAt": expires_at,
            }
        )
        if idempotency_key is not UNSET:
            field_dict["idempotencyKey"] = idempotency_key
        if created_at is not UNSET:
            field_dict["createdAt"] = created_at
        if execution_mode is not UNSET:
            field_dict["executionMode"] = execution_mode
        if canonical_state is not UNSET:
            field_dict["canonicalState"] = canonical_state
        if external_state is not UNSET:
            field_dict["externalState"] = external_state
        if recommend_only_state is not UNSET:
            field_dict["recommendOnlyState"] = recommend_only_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        recommendation_id = UUID(d.pop("recommendationId"))

        version = d.pop("version")

        state = ApprovalState(d.pop("state"))

        price = MoneyAmount.from_dict(d.pop("price"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        idempotency_key = d.pop("idempotencyKey", UNSET)

        _created_at = d.pop("createdAt", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _execution_mode = d.pop("executionMode", UNSET)
        execution_mode: ExecutionMode | Unset
        if isinstance(_execution_mode, Unset):
            execution_mode = UNSET
        else:
            execution_mode = ExecutionMode(_execution_mode)

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

        action_summary = cls(
            id=id,
            recommendation_id=recommendation_id,
            version=version,
            state=state,
            price=price,
            expires_at=expires_at,
            idempotency_key=idempotency_key,
            created_at=created_at,
            execution_mode=execution_mode,
            canonical_state=canonical_state,
            external_state=external_state,
            recommend_only_state=recommend_only_state,
        )

        return action_summary

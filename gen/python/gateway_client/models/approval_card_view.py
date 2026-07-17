from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.approval_state import ApprovalState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.approval_binding import ApprovalBinding
    from ..models.approval_state_history_entry import ApprovalStateHistoryEntry
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="ApprovalCardView")


@_attrs_define
class ApprovalCardView:
    """A versioned approval card (APR-001) with its current §8.4 state, its bound control versions, its authoritative Money
    price, and its append-only history. A card carries a control ONLY in `awaiting_confirmation`.

        Attributes:
            id (UUID):
            recommendation_id (UUID):
            version (int):
            state (ApprovalState): One node of the §8.4 approval state machine. The set is closed; it is the authoritative
                lifecycle vocabulary for a card and its history.
            binding (ApprovalBinding): The APR-001 version binding of an approval control: the exact action id,
                parameter/context/policy/cost versions, evidence versions, and expiry. ANY change to a bound dimension, or a
                reached expiry, invalidates the control.
            price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
                mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            has_control (bool): True only when the card is in awaiting_confirmation (a live control).
            history (list[ApprovalStateHistoryEntry]):
            idempotency_key (str | Unset): Stable execution hand-off key (EXE-002 seam); execution is S18.
    """

    id: UUID
    recommendation_id: UUID
    version: int
    state: ApprovalState
    binding: ApprovalBinding
    price: MoneyAmount
    has_control: bool
    history: list[ApprovalStateHistoryEntry]
    idempotency_key: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        recommendation_id = str(self.recommendation_id)

        version = self.version

        state = self.state.value

        binding = self.binding.to_dict()

        price = self.price.to_dict()

        has_control = self.has_control

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        idempotency_key = self.idempotency_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "recommendationId": recommendation_id,
                "version": version,
                "state": state,
                "binding": binding,
                "price": price,
                "hasControl": has_control,
                "history": history,
            }
        )
        if idempotency_key is not UNSET:
            field_dict["idempotencyKey"] = idempotency_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.approval_binding import ApprovalBinding
        from ..models.approval_state_history_entry import ApprovalStateHistoryEntry
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        recommendation_id = UUID(d.pop("recommendationId"))

        version = d.pop("version")

        state = ApprovalState(d.pop("state"))

        binding = ApprovalBinding.from_dict(d.pop("binding"))

        price = MoneyAmount.from_dict(d.pop("price"))

        has_control = d.pop("hasControl")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = ApprovalStateHistoryEntry.from_dict(history_item_data)

            history.append(history_item)

        idempotency_key = d.pop("idempotencyKey", UNSET)

        approval_card_view = cls(
            id=id,
            recommendation_id=recommendation_id,
            version=version,
            state=state,
            binding=binding,
            price=price,
            has_control=has_control,
            history=history,
            idempotency_key=idempotency_key,
        )

        return approval_card_view

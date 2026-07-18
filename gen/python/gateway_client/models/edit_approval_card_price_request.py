from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="EditApprovalCardPriceRequest")


@_attrs_define
class EditApprovalCardPriceRequest:
    """The CHAT-044 price edit request.

    Attributes:
        card_id (UUID):
        new_price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value
            = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
            representable because the account's entry currency is known; it stays excluded from executable paths until
            S16+S35.
    """

    card_id: UUID
    new_price: MoneyAmount

    def to_dict(self) -> dict[str, Any]:
        card_id = str(self.card_id)

        new_price = self.new_price.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cardId": card_id,
                "newPrice": new_price,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        card_id = UUID(d.pop("cardId"))

        new_price = MoneyAmount.from_dict(d.pop("newPrice"))

        edit_approval_card_price_request = cls(
            card_id=card_id,
            new_price=new_price,
        )

        return edit_approval_card_price_request

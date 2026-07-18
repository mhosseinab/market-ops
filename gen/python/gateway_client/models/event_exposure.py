from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="EventExposure")


@_attrs_define
class EventExposure:
    """An event's business impact (PRD §7.4 EVT-004/EVT-005). It is EITHER a known Money amount (derived from margin/sales
    context) OR explicitly unknown. When `known` is false there is NO `amount` at all — a missing sales/cost context is
    never a fabricated number (EVT-005).

        Attributes:
            known (bool): Whether a numeric exposure exists. False ⇒ impact unknown.
            amount (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
    """

    known: bool
    amount: MoneyAmount | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        known = self.known

        amount: dict[str, Any] | Unset = UNSET
        if not isinstance(self.amount, Unset):
            amount = self.amount.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "known": known,
            }
        )
        if amount is not UNSET:
            field_dict["amount"] = amount

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        known = d.pop("known")

        _amount = d.pop("amount", UNSET)
        amount: MoneyAmount | Unset
        if isinstance(_amount, Unset):
            amount = UNSET
        else:
            amount = MoneyAmount.from_dict(_amount)

        event_exposure = cls(
            known=known,
            amount=amount,
        )

        return event_exposure

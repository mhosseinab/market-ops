from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="PolicyProposal")


@_attrs_define
class PolicyProposal:
    """An accepted policy result: a proposed price and its contribution. Present only when every hard stage passed and the
    contribution is strictly positive. It is NOT an approval control.

        Attributes:
            price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
                mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            contribution (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
    """

    price: MoneyAmount
    contribution: MoneyAmount

    def to_dict(self) -> dict[str, Any]:
        price = self.price.to_dict()

        contribution = self.contribution.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "price": price,
                "contribution": contribution,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        price = MoneyAmount.from_dict(d.pop("price"))

        contribution = MoneyAmount.from_dict(d.pop("contribution"))

        policy_proposal = cls(
            price=price,
            contribution=contribution,
        )

        return policy_proposal

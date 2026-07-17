from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="PolicyBoundary")


@_attrs_define
class PolicyBoundary:
    """The marketplace price boundary (stage 1, §9.2). `known` false is an UNKNOWN boundary and blocks (§16). Min/Max are
    required when known.

        Attributes:
            known (bool):
            min_ (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            max_ (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
    """

    known: bool
    min_: MoneyAmount | Unset = UNSET
    max_: MoneyAmount | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        known = self.known

        min_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.min_, Unset):
            min_ = self.min_.to_dict()

        max_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.max_, Unset):
            max_ = self.max_.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "known": known,
            }
        )
        if min_ is not UNSET:
            field_dict["min"] = min_
        if max_ is not UNSET:
            field_dict["max"] = max_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        known = d.pop("known")

        _min_ = d.pop("min", UNSET)
        min_: MoneyAmount | Unset
        if isinstance(_min_, Unset):
            min_ = UNSET
        else:
            min_ = MoneyAmount.from_dict(_min_)

        _max_ = d.pop("max", UNSET)
        max_: MoneyAmount | Unset
        if isinstance(_max_, Unset):
            max_ = UNSET
        else:
            max_ = MoneyAmount.from_dict(_max_)

        policy_boundary = cls(
            known=known,
            min_=min_,
            max_=max_,
        )

        return policy_boundary

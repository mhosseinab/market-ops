from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MoneyAmount")


@_attrs_define
class MoneyAmount:
    """An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value = mantissa × 10^exponent
    currency units. There is NO float: mantissa is an exact integer. A cost amount is representable because the
    account's entry currency is known; it stays excluded from executable paths until S16+S35.

        Attributes:
            mantissa (str): Exact int64 mantissa as a signed base-10 decimal STRING (PRD §9.1, never-cut MONEY CORRECTNESS).
                String-encoded — never a JSON number — so full int64 precision survives every generated client boundary (a
                JavaScript number silently rounds int64 values above 2^53). Consumers MUST convert the string directly to a big
                integer and reject any value outside the signed int64 range or not matching `^-?[0-9]+$`; there is NO float on
                any money path.
            currency (str): ISO-4217 currency code.
            exponent (int): Base-10 exponent applied to the mantissa.
    """

    mantissa: str
    currency: str
    exponent: int

    def to_dict(self) -> dict[str, Any]:
        mantissa = self.mantissa

        currency = self.currency

        exponent = self.exponent

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mantissa": mantissa,
                "currency": currency,
                "exponent": exponent,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mantissa = d.pop("mantissa")

        currency = d.pop("currency")

        exponent = d.pop("exponent")

        money_amount = cls(
            mantissa=mantissa,
            currency=currency,
            exponent=exponent,
        )

        return money_amount

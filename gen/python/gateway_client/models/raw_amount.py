from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RawAmount")


@_attrs_define
class RawAmount:
    """Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim and NEVER promoted to Money: no
    currency, no exponent, no conversion. The source unit is validation-gated (Gate 0a) and unknown; an absent unit
    token stays quarantined, never inferred.

        Attributes:
            text (str): The amount exactly as captured, before any normalization.
            value (str): The parsed numeric token as raw source text (never a number type).
            unit (str): The source unit token as captured; not interpreted as ISO-4217.
    """

    text: str
    value: str
    unit: str

    def to_dict(self) -> dict[str, Any]:
        text = self.text

        value = self.value

        unit = self.unit

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "text": text,
                "value": value,
                "unit": unit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        text = d.pop("text")

        value = d.pop("value")

        unit = d.pop("unit")

        raw_amount = cls(
            text=text,
            value=value,
            unit=unit,
        )

        return raw_amount

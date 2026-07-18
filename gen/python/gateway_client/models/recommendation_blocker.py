from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RecommendationBlocker")


@_attrs_define
class RecommendationBlocker:
    """One typed PRC-002 reason no approval control exists (in policy order). The code is stable and machine-readable; the
    message carries no authority (§8).

        Attributes:
            code (str):
            message (str):
    """

    code: str
    message: str

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        message = self.message

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        recommendation_blocker = cls(
            code=code,
            message=message,
        )

        return recommendation_blocker

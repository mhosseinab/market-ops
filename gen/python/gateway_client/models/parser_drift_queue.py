from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ParserDriftQueue")


@_attrs_define
class ParserDriftQueue:
    """The Route C parser/schema-drift queue. NOT YET backed by a persisted store (§10.4) — `available` is false with an
    explicit reason rather than a fabricated empty success, per the screens-only-fallback / unavailable-with-reason
    posture (PRC-001 optionality). Closing this is a named follow-up on the Route C observer plane.

        Attributes:
            available (bool):
            items (list[Any]):
            reason (str | Unset):
    """

    available: bool
    items: list[Any]
    reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        items = self.items

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "available": available,
                "items": items,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        available = d.pop("available")

        items = cast(list[Any], d.pop("items"))

        reason = d.pop("reason", UNSET)

        parser_drift_queue = cls(
            available=available,
            items=items,
            reason=reason,
        )

        return parser_drift_queue

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="BriefingEvent")


@_attrs_define
class BriefingEvent:
    """One ranked event in a daily briefing. `rank` is 1-based and matches the Today feed position; `eventId` matches the
    Today feed event id.

        Attributes:
            rank (int):
            event_id (UUID):
            event_type (str):
            severity (str):
    """

    rank: int
    event_id: UUID
    event_type: str
    severity: str

    def to_dict(self) -> dict[str, Any]:
        rank = self.rank

        event_id = str(self.event_id)

        event_type = self.event_type

        severity = self.severity

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rank": rank,
                "eventId": event_id,
                "eventType": event_type,
                "severity": severity,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rank = d.pop("rank")

        event_id = UUID(d.pop("eventId"))

        event_type = d.pop("eventType")

        severity = d.pop("severity")

        briefing_event = cls(
            rank=rank,
            event_id=event_id,
            event_type=event_type,
            severity=severity,
        )

        return briefing_event

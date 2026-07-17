from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_relevance_kind import EventRelevanceKind

T = TypeVar("T", bound="EventRelevanceRecorded")


@_attrs_define
class EventRelevanceRecorded:
    """
    Attributes:
        id (UUID):
        event_id (UUID):
        relevance (EventRelevanceKind): The closed relevance-feedback set (EVT-005).
        created_at (datetime.datetime):
    """

    id: UUID
    event_id: UUID
    relevance: EventRelevanceKind
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        event_id = str(self.event_id)

        relevance = self.relevance.value

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "eventId": event_id,
                "relevance": relevance,
                "createdAt": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        event_id = UUID(d.pop("eventId"))

        relevance = EventRelevanceKind(d.pop("relevance"))

        created_at = datetime.datetime.fromisoformat(d.pop("createdAt"))

        event_relevance_recorded = cls(
            id=id,
            event_id=event_id,
            relevance=relevance,
            created_at=created_at,
        )

        return event_relevance_recorded

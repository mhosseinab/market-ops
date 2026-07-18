from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_relevance_kind import EventRelevanceKind
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRelevanceRequest")


@_attrs_define
class EventRelevanceRequest:
    """Record relevance feedback on a market event (EVT-005, append-only). Never approves or executes anything.

    Attributes:
        event_id (UUID):
        relevance (EventRelevanceKind): The closed relevance-feedback set (EVT-005).
        note (str | Unset): Optional free-text note. Carries no authority (§8 free-text containment).
    """

    event_id: UUID
    relevance: EventRelevanceKind
    note: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        relevance = self.relevance.value

        note = self.note

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "eventId": event_id,
                "relevance": relevance,
            }
        )
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("eventId"))

        relevance = EventRelevanceKind(d.pop("relevance"))

        note = d.pop("note", UNSET)

        event_relevance_request = cls(
            event_id=event_id,
            relevance=relevance,
            note=note,
        )

        return event_relevance_request

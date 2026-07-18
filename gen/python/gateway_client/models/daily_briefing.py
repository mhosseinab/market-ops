from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.briefing_event import BriefingEvent


T = TypeVar("T", bound="DailyBriefing")


@_attrs_define
class DailyBriefing:
    """The stored once-per-business-day briefing (CHAT-010). Its events carry the SAME ids and ORDER as the Today feed for
    the account/day (generated from the one ranking, never a re-computation).

        Attributes:
            marketplace_account_id (UUID):
            business_day (datetime.date):
            generated_at (datetime.datetime):
            events (list[BriefingEvent]):
    """

    marketplace_account_id: UUID
    business_day: datetime.date
    generated_at: datetime.datetime
    events: list[BriefingEvent]

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        business_day = self.business_day.isoformat()

        generated_at = self.generated_at.isoformat()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "businessDay": business_day,
                "generatedAt": generated_at,
                "events": events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.briefing_event import BriefingEvent

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        business_day = datetime.date.fromisoformat(d.pop("businessDay"))

        generated_at = datetime.datetime.fromisoformat(d.pop("generatedAt"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = BriefingEvent.from_dict(events_item_data)

            events.append(events_item)

        daily_briefing = cls(
            marketplace_account_id=marketplace_account_id,
            business_day=business_day,
            generated_at=generated_at,
            events=events,
        )

        return daily_briefing

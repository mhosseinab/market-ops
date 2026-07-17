from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_rank_factors import EventRankFactors
    from ..models.market_event import MarketEvent


T = TypeVar("T", bound="RankedEvent")


@_attrs_define
class RankedEvent:
    """One event placed in the deterministic Today order (EVT-004). Rank is 1-based; factors exposes all three ranking
    inputs.

        Attributes:
            event (MarketEvent): A market event lifecycle record (PRD §7.4, §15.1). It cites its observation evidence with
                the observed quality state as-is (never upgraded) and carries its versioned materiality-threshold provenance
                (EVT-002). Exposure obeys EVT-005.
            rank (int): 1-based deterministic rank in the Today feed.
            factors (EventRankFactors): The three EVT-004 ranking factors for one event, exposed so the UI can show why an
                event ranks where it does. Confidence and urgency are basis points (0..10000); exposure is the EventExposure
                (unknown stays unknown).
    """

    event: MarketEvent
    rank: int
    factors: EventRankFactors

    def to_dict(self) -> dict[str, Any]:
        event = self.event.to_dict()

        rank = self.rank

        factors = self.factors.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event": event,
                "rank": rank,
                "factors": factors,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_rank_factors import EventRankFactors
        from ..models.market_event import MarketEvent

        d = dict(src_dict)
        event = MarketEvent.from_dict(d.pop("event"))

        rank = d.pop("rank")

        factors = EventRankFactors.from_dict(d.pop("factors"))

        ranked_event = cls(
            event=event,
            rank=rank,
            factors=factors,
        )

        return ranked_event

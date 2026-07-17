from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_exposure import EventExposure


T = TypeVar("T", bound="EventRankFactors")


@_attrs_define
class EventRankFactors:
    """The three EVT-004 ranking factors for one event, exposed so the UI can show why an event ranks where it does.
    Confidence and urgency are basis points (0..10000); exposure is the EventExposure (unknown stays unknown).

        Attributes:
            exposure (EventExposure): An event's business impact (PRD §7.4 EVT-004/EVT-005). It is EITHER a known Money
                amount (derived from margin/sales context) OR explicitly unknown. When `known` is false there is NO `amount` at
                all — a missing sales/cost context is never a fabricated number (EVT-005).
            confidence_bp (int): Confidence factor in basis points (0..10000), from evidence quality.
            urgency_bp (int): Urgency factor in basis points (0..10000), from severity.
    """

    exposure: EventExposure
    confidence_bp: int
    urgency_bp: int

    def to_dict(self) -> dict[str, Any]:
        exposure = self.exposure.to_dict()

        confidence_bp = self.confidence_bp

        urgency_bp = self.urgency_bp

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "exposure": exposure,
                "confidenceBp": confidence_bp,
                "urgencyBp": urgency_bp,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_exposure import EventExposure

        d = dict(src_dict)
        exposure = EventExposure.from_dict(d.pop("exposure"))

        confidence_bp = d.pop("confidenceBp")

        urgency_bp = d.pop("urgencyBp")

        event_rank_factors = cls(
            exposure=exposure,
            confidence_bp=confidence_bp,
            urgency_bp=urgency_bp,
        )

        return event_rank_factors

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.availability_status import AvailabilityStatus
from ..models.observation_route import ObservationRoute

T = TypeVar("T", bound="ConflictRouteEvidence")


@_attrs_define
class ConflictRouteEvidence:
    """One route's latest in-window observation behind a conflict (issue #94): route provenance plus the raw price
    value/unit (money quarantine §9.1, never promoted to Money), availability, and capture/freshness times. Exposed
    verbatim from the existing per-route in-window query — no recompute.

        Attributes:
            route (ObservationRoute): Capture route provenance (PRD §10.1). route_a official connector, route_b extension
                (corroboration only), route_c server observation.
            value (str): The parsed numeric token as raw source text (never a number type).
            unit (str): The source unit token as captured; not interpreted as ISO-4217.
            availability_status (AvailabilityStatus): Normalized availability (docs/11, §16). `unavailable` is the DISTINCT
                temporary-out state; `disappeared` is the permanent close (offer gone, closed with an end time, never a zero
                price).
            captured_at (datetime.datetime): Capture time of this route's latest in-window observation (UTC).
            freshness_deadline (datetime.datetime): When this route's value expires (OBS-004).
    """

    route: ObservationRoute
    value: str
    unit: str
    availability_status: AvailabilityStatus
    captured_at: datetime.datetime
    freshness_deadline: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        route = self.route.value

        value = self.value

        unit = self.unit

        availability_status = self.availability_status.value

        captured_at = self.captured_at.isoformat()

        freshness_deadline = self.freshness_deadline.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "route": route,
                "value": value,
                "unit": unit,
                "availabilityStatus": availability_status,
                "capturedAt": captured_at,
                "freshnessDeadline": freshness_deadline,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = ObservationRoute(d.pop("route"))

        value = d.pop("value")

        unit = d.pop("unit")

        availability_status = AvailabilityStatus(d.pop("availabilityStatus"))

        captured_at = datetime.datetime.fromisoformat(d.pop("capturedAt"))

        freshness_deadline = datetime.datetime.fromisoformat(d.pop("freshnessDeadline"))

        conflict_route_evidence = cls(
            route=route,
            value=value,
            unit=unit,
            availability_status=availability_status,
            captured_at=captured_at,
            freshness_deadline=freshness_deadline,
        )

        return conflict_route_evidence

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.availability_status import AvailabilityStatus
from ..models.observation_route import ObservationRoute
from ..models.quality_state import QualityState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.raw_amount import RawAmount


T = TypeVar("T", bound="ObservedOffer")


@_attrs_define
class ObservedOffer:
    """A derived CURRENT Observed Offer (PRD §7.3, §10.3): the latest accepted observation's fields, quality, freshness
    deadline, and corroborating route provenance. Price is raw evidence only. When `endedAt` is set the offer has
    disappeared and is closed (§16) — the last raw price is retained, never zeroed.

        Attributes:
            id (UUID):
            target_id (UUID):
            marketplace_account_id (UUID):
            offer_identity (str): Canonical per-offer key (native variant id + seller); LTR-isolated.
            native_variant_id (int):
            price (RawAmount): Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim and NEVER
                promoted to Money: no currency, no exponent, no conversion. The source unit is validation-gated (Gate 0a) and
                unknown; an absent unit token stays quarantined, never inferred.
            list_price (RawAmount): Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim and NEVER
                promoted to Money: no currency, no exponent, no conversion. The source unit is validation-gated (Gate 0a) and
                unknown; an absent unit token stays quarantined, never inferred.
            availability_status (AvailabilityStatus): Normalized availability (docs/11, §16). `unavailable` is the DISTINCT
                temporary-out state; `disappeared` is the permanent close (offer gone, closed with an end time, never a zero
                price).
            quality (QualityState): The SIX evidence-quality states (PRD §10.3, OBS-003). The set is closed; each state has
                a fixed display/recommend/execute consequence. An expired value is `stale` and can never satisfy a current-data
                gate (OBS-004).
            captured_at (datetime.datetime): Capture time of the current observation (UTC).
            freshness_deadline (datetime.datetime): When this value expires; past it the offer is Stale (OBS-004).
            routes (list[ObservationRoute]): The routes corroborating the current value (provenance, OBS-008).
            native_seller_id (str | Unset):
            stock_signal (int | None | Unset): Optional observed stock signal; null when DK omits it.
            ended_at (datetime.datetime | None | Unset): Offer-disappearance close time (§16); null while live.
    """

    id: UUID
    target_id: UUID
    marketplace_account_id: UUID
    offer_identity: str
    native_variant_id: int
    price: RawAmount
    list_price: RawAmount
    availability_status: AvailabilityStatus
    quality: QualityState
    captured_at: datetime.datetime
    freshness_deadline: datetime.datetime
    routes: list[ObservationRoute]
    native_seller_id: str | Unset = UNSET
    stock_signal: int | None | Unset = UNSET
    ended_at: datetime.datetime | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        target_id = str(self.target_id)

        marketplace_account_id = str(self.marketplace_account_id)

        offer_identity = self.offer_identity

        native_variant_id = self.native_variant_id

        price = self.price.to_dict()

        list_price = self.list_price.to_dict()

        availability_status = self.availability_status.value

        quality = self.quality.value

        captured_at = self.captured_at.isoformat()

        freshness_deadline = self.freshness_deadline.isoformat()

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.value
            routes.append(routes_item)

        native_seller_id = self.native_seller_id

        stock_signal: int | None | Unset
        if isinstance(self.stock_signal, Unset):
            stock_signal = UNSET
        else:
            stock_signal = self.stock_signal

        ended_at: None | str | Unset
        if isinstance(self.ended_at, Unset):
            ended_at = UNSET
        elif isinstance(self.ended_at, datetime.datetime):
            ended_at = self.ended_at.isoformat()
        else:
            ended_at = self.ended_at

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "targetId": target_id,
                "marketplaceAccountId": marketplace_account_id,
                "offerIdentity": offer_identity,
                "nativeVariantId": native_variant_id,
                "price": price,
                "listPrice": list_price,
                "availabilityStatus": availability_status,
                "quality": quality,
                "capturedAt": captured_at,
                "freshnessDeadline": freshness_deadline,
                "routes": routes,
            }
        )
        if native_seller_id is not UNSET:
            field_dict["nativeSellerId"] = native_seller_id
        if stock_signal is not UNSET:
            field_dict["stockSignal"] = stock_signal
        if ended_at is not UNSET:
            field_dict["endedAt"] = ended_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.raw_amount import RawAmount

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        target_id = UUID(d.pop("targetId"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        offer_identity = d.pop("offerIdentity")

        native_variant_id = d.pop("nativeVariantId")

        price = RawAmount.from_dict(d.pop("price"))

        list_price = RawAmount.from_dict(d.pop("listPrice"))

        availability_status = AvailabilityStatus(d.pop("availabilityStatus"))

        quality = QualityState(d.pop("quality"))

        captured_at = datetime.datetime.fromisoformat(d.pop("capturedAt"))

        freshness_deadline = datetime.datetime.fromisoformat(d.pop("freshnessDeadline"))

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = ObservationRoute(routes_item_data)

            routes.append(routes_item)

        native_seller_id = d.pop("nativeSellerId", UNSET)

        def _parse_stock_signal(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        stock_signal = _parse_stock_signal(d.pop("stockSignal", UNSET))

        def _parse_ended_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                ended_at_type_0 = datetime.datetime.fromisoformat(data)

                return ended_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        ended_at = _parse_ended_at(d.pop("endedAt", UNSET))

        observed_offer = cls(
            id=id,
            target_id=target_id,
            marketplace_account_id=marketplace_account_id,
            offer_identity=offer_identity,
            native_variant_id=native_variant_id,
            price=price,
            list_price=list_price,
            availability_status=availability_status,
            quality=quality,
            captured_at=captured_at,
            freshness_deadline=freshness_deadline,
            routes=routes,
            native_seller_id=native_seller_id,
            stock_signal=stock_signal,
            ended_at=ended_at,
        )

        return observed_offer

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


T = TypeVar("T", bound="Observation")


@_attrs_define
class Observation:
    """One append-only observation evidence row (PRD §7.3 OBS-002). Carries the full evidence envelope; historical rows
    never silently become current.

        Attributes:
            id (UUID):
            target_id (UUID):
            marketplace_account_id (UUID):
            offer_identity (str):
            route (ObservationRoute): Capture route provenance (PRD §10.1). route_a official connector, route_b extension
                (corroboration only), route_c server observation.
            parser_version (str):
            source_type (str):
            evidence_ref (str):
            price (RawAmount): Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim and NEVER
                promoted to Money: no currency, no exponent, no conversion. The source unit is validation-gated (Gate 0a) and
                unknown; an absent unit token stays quarantined, never inferred.
            availability_status (AvailabilityStatus): Normalized availability (docs/11, §16). `unavailable` is the DISTINCT
                temporary-out state; `disappeared` is the permanent close (offer gone, closed with an end time, never a zero
                price).
            quality (QualityState): The SIX evidence-quality states (PRD §10.3, OBS-003). The set is closed; each state has
                a fixed display/recommend/execute consequence. An expired value is `stale` and can never satisfy a current-data
                gate (OBS-004).
            captured_at (datetime.datetime):
            freshness_deadline (datetime.datetime):
            native_variant_id (int | Unset):
            native_seller_id (str | Unset):
            sub_route (str | Unset):
            connector_version (str | Unset):
            source_url (str | Unset):
            list_price (RawAmount | Unset): Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim
                and NEVER promoted to Money: no currency, no exponent, no conversion. The source unit is validation-gated (Gate
                0a) and unknown; an absent unit token stays quarantined, never inferred.
            stock_signal (int | None | Unset):
    """

    id: UUID
    target_id: UUID
    marketplace_account_id: UUID
    offer_identity: str
    route: ObservationRoute
    parser_version: str
    source_type: str
    evidence_ref: str
    price: RawAmount
    availability_status: AvailabilityStatus
    quality: QualityState
    captured_at: datetime.datetime
    freshness_deadline: datetime.datetime
    native_variant_id: int | Unset = UNSET
    native_seller_id: str | Unset = UNSET
    sub_route: str | Unset = UNSET
    connector_version: str | Unset = UNSET
    source_url: str | Unset = UNSET
    list_price: RawAmount | Unset = UNSET
    stock_signal: int | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        target_id = str(self.target_id)

        marketplace_account_id = str(self.marketplace_account_id)

        offer_identity = self.offer_identity

        route = self.route.value

        parser_version = self.parser_version

        source_type = self.source_type

        evidence_ref = self.evidence_ref

        price = self.price.to_dict()

        availability_status = self.availability_status.value

        quality = self.quality.value

        captured_at = self.captured_at.isoformat()

        freshness_deadline = self.freshness_deadline.isoformat()

        native_variant_id = self.native_variant_id

        native_seller_id = self.native_seller_id

        sub_route = self.sub_route

        connector_version = self.connector_version

        source_url = self.source_url

        list_price: dict[str, Any] | Unset = UNSET
        if not isinstance(self.list_price, Unset):
            list_price = self.list_price.to_dict()

        stock_signal: int | None | Unset
        if isinstance(self.stock_signal, Unset):
            stock_signal = UNSET
        else:
            stock_signal = self.stock_signal

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "targetId": target_id,
                "marketplaceAccountId": marketplace_account_id,
                "offerIdentity": offer_identity,
                "route": route,
                "parserVersion": parser_version,
                "sourceType": source_type,
                "evidenceRef": evidence_ref,
                "price": price,
                "availabilityStatus": availability_status,
                "quality": quality,
                "capturedAt": captured_at,
                "freshnessDeadline": freshness_deadline,
            }
        )
        if native_variant_id is not UNSET:
            field_dict["nativeVariantId"] = native_variant_id
        if native_seller_id is not UNSET:
            field_dict["nativeSellerId"] = native_seller_id
        if sub_route is not UNSET:
            field_dict["subRoute"] = sub_route
        if connector_version is not UNSET:
            field_dict["connectorVersion"] = connector_version
        if source_url is not UNSET:
            field_dict["sourceUrl"] = source_url
        if list_price is not UNSET:
            field_dict["listPrice"] = list_price
        if stock_signal is not UNSET:
            field_dict["stockSignal"] = stock_signal

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.raw_amount import RawAmount

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        target_id = UUID(d.pop("targetId"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        offer_identity = d.pop("offerIdentity")

        route = ObservationRoute(d.pop("route"))

        parser_version = d.pop("parserVersion")

        source_type = d.pop("sourceType")

        evidence_ref = d.pop("evidenceRef")

        price = RawAmount.from_dict(d.pop("price"))

        availability_status = AvailabilityStatus(d.pop("availabilityStatus"))

        quality = QualityState(d.pop("quality"))

        captured_at = datetime.datetime.fromisoformat(d.pop("capturedAt"))

        freshness_deadline = datetime.datetime.fromisoformat(d.pop("freshnessDeadline"))

        native_variant_id = d.pop("nativeVariantId", UNSET)

        native_seller_id = d.pop("nativeSellerId", UNSET)

        sub_route = d.pop("subRoute", UNSET)

        connector_version = d.pop("connectorVersion", UNSET)

        source_url = d.pop("sourceUrl", UNSET)

        _list_price = d.pop("listPrice", UNSET)
        list_price: RawAmount | Unset
        if isinstance(_list_price, Unset):
            list_price = UNSET
        else:
            list_price = RawAmount.from_dict(_list_price)

        def _parse_stock_signal(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        stock_signal = _parse_stock_signal(d.pop("stockSignal", UNSET))

        observation = cls(
            id=id,
            target_id=target_id,
            marketplace_account_id=marketplace_account_id,
            offer_identity=offer_identity,
            route=route,
            parser_version=parser_version,
            source_type=source_type,
            evidence_ref=evidence_ref,
            price=price,
            availability_status=availability_status,
            quality=quality,
            captured_at=captured_at,
            freshness_deadline=freshness_deadline,
            native_variant_id=native_variant_id,
            native_seller_id=native_seller_id,
            sub_route=sub_route,
            connector_version=connector_version,
            source_url=source_url,
            list_price=list_price,
            stock_signal=stock_signal,
        )

        return observation

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.capture_upload_availability_status import CaptureUploadAvailabilityStatus
from ..models.capture_upload_confidence import CaptureUploadConfidence
from ..models.capture_upload_source_type import CaptureUploadSourceType
from ..models.capture_upload_sub_route import CaptureUploadSubRoute
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.raw_amount import RawAmount


T = TypeVar("T", bound="CaptureUpload")


@_attrs_define
class CaptureUpload:
    """ALLOW-LISTED extension (Route B) capture upload (PRD §10.1). Only these fields are accepted (additionalProperties
    false). The extension cannot assert schema/identity validity or conflict, cannot forge Route C, and cannot declare a
    permanent disappearance — those are server-side. Price is raw evidence only (money quarantine).

        Attributes:
            marketplace_account_id (UUID):
            target_id (UUID): The confirmed-identity observation target this capture is for.
            native_variant_id (int):
            sub_route (CaptureUploadSubRoute): Route B sub-route (PRD §7.3 OBS-005).
            source_type (CaptureUploadSourceType): How the extension captured the value.
            parser_version (str):
            evidence_ref (str):
            availability_status (CaptureUploadAvailabilityStatus): Extension-observable availability; disappearance close is
                server-side.
            captured_at (datetime.datetime):
            confidence (CaptureUploadConfidence): Capture parser/unit confidence (docs/08).
            native_seller_id (str | Unset):
            source_url (str | Unset):
            connector_version (str | Unset):
            raw_fixture_ref (str | Unset):
            price (RawAmount | Unset): Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim and
                NEVER promoted to Money: no currency, no exponent, no conversion. The source unit is validation-gated (Gate 0a)
                and unknown; an absent unit token stays quarantined, never inferred.
            list_price (RawAmount | Unset): Raw marketplace price evidence (PRD §9.1 money quarantine). Preserved verbatim
                and NEVER promoted to Money: no currency, no exponent, no conversion. The source unit is validation-gated (Gate
                0a) and unknown; an absent unit token stays quarantined, never inferred.
            stock_signal (int | None | Unset):
    """

    marketplace_account_id: UUID
    target_id: UUID
    native_variant_id: int
    sub_route: CaptureUploadSubRoute
    source_type: CaptureUploadSourceType
    parser_version: str
    evidence_ref: str
    availability_status: CaptureUploadAvailabilityStatus
    captured_at: datetime.datetime
    confidence: CaptureUploadConfidence
    native_seller_id: str | Unset = UNSET
    source_url: str | Unset = UNSET
    connector_version: str | Unset = UNSET
    raw_fixture_ref: str | Unset = UNSET
    price: RawAmount | Unset = UNSET
    list_price: RawAmount | Unset = UNSET
    stock_signal: int | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        target_id = str(self.target_id)

        native_variant_id = self.native_variant_id

        sub_route = self.sub_route.value

        source_type = self.source_type.value

        parser_version = self.parser_version

        evidence_ref = self.evidence_ref

        availability_status = self.availability_status.value

        captured_at = self.captured_at.isoformat()

        confidence = self.confidence.value

        native_seller_id = self.native_seller_id

        source_url = self.source_url

        connector_version = self.connector_version

        raw_fixture_ref = self.raw_fixture_ref

        price: dict[str, Any] | Unset = UNSET
        if not isinstance(self.price, Unset):
            price = self.price.to_dict()

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
                "marketplaceAccountId": marketplace_account_id,
                "targetId": target_id,
                "nativeVariantId": native_variant_id,
                "subRoute": sub_route,
                "sourceType": source_type,
                "parserVersion": parser_version,
                "evidenceRef": evidence_ref,
                "availabilityStatus": availability_status,
                "capturedAt": captured_at,
                "confidence": confidence,
            }
        )
        if native_seller_id is not UNSET:
            field_dict["nativeSellerId"] = native_seller_id
        if source_url is not UNSET:
            field_dict["sourceUrl"] = source_url
        if connector_version is not UNSET:
            field_dict["connectorVersion"] = connector_version
        if raw_fixture_ref is not UNSET:
            field_dict["rawFixtureRef"] = raw_fixture_ref
        if price is not UNSET:
            field_dict["price"] = price
        if list_price is not UNSET:
            field_dict["listPrice"] = list_price
        if stock_signal is not UNSET:
            field_dict["stockSignal"] = stock_signal

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.raw_amount import RawAmount

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        target_id = UUID(d.pop("targetId"))

        native_variant_id = d.pop("nativeVariantId")

        sub_route = CaptureUploadSubRoute(d.pop("subRoute"))

        source_type = CaptureUploadSourceType(d.pop("sourceType"))

        parser_version = d.pop("parserVersion")

        evidence_ref = d.pop("evidenceRef")

        availability_status = CaptureUploadAvailabilityStatus(d.pop("availabilityStatus"))

        captured_at = datetime.datetime.fromisoformat(d.pop("capturedAt"))

        confidence = CaptureUploadConfidence(d.pop("confidence"))

        native_seller_id = d.pop("nativeSellerId", UNSET)

        source_url = d.pop("sourceUrl", UNSET)

        connector_version = d.pop("connectorVersion", UNSET)

        raw_fixture_ref = d.pop("rawFixtureRef", UNSET)

        _price = d.pop("price", UNSET)
        price: RawAmount | Unset
        if isinstance(_price, Unset):
            price = UNSET
        else:
            price = RawAmount.from_dict(_price)

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

        capture_upload = cls(
            marketplace_account_id=marketplace_account_id,
            target_id=target_id,
            native_variant_id=native_variant_id,
            sub_route=sub_route,
            source_type=source_type,
            parser_version=parser_version,
            evidence_ref=evidence_ref,
            availability_status=availability_status,
            captured_at=captured_at,
            confidence=confidence,
            native_seller_id=native_seller_id,
            source_url=source_url,
            connector_version=connector_version,
            raw_fixture_ref=raw_fixture_ref,
            price=price,
            list_price=list_price,
            stock_signal=stock_signal,
        )

        return capture_upload

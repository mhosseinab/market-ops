from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.cost_component import CostComponent
from ..types import UNSET, Unset

T = TypeVar("T", bound="SingleCostEntryRequest")


@_attrs_define
class SingleCostEntryRequest:
    """Record one cost-component value for a SKU (CST-002). effectiveFrom defaults to now; staleAfter is an optional
    review-by instant.

        Attributes:
            marketplace_account_id (UUID):
            variant_id (UUID):
            component (CostComponent): A cost component of the §9.2 contribution model. The set is closed. COGS and
                commission are always required; fulfillment/shipping/promotion are required when applicable to the listing;
                packaging/ads/returns are optional in P0 (an account policy may still require them).
            raw_value (str): The value as entered (Persian/Latin digits normalized, LOC-007).
            raw_unit (str | Unset): The unit as entered, preserved verbatim as evidence.
            effective_from (datetime.datetime | Unset): When this version takes effect (RFC 3339). Defaults to now.
            stale_after (datetime.datetime | None | Unset): Optional review-by instant; past ⇒ the value is stale for
                readiness.
    """

    marketplace_account_id: UUID
    variant_id: UUID
    component: CostComponent
    raw_value: str
    raw_unit: str | Unset = UNSET
    effective_from: datetime.datetime | Unset = UNSET
    stale_after: datetime.datetime | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        component = self.component.value

        raw_value = self.raw_value

        raw_unit = self.raw_unit

        effective_from: str | Unset = UNSET
        if not isinstance(self.effective_from, Unset):
            effective_from = self.effective_from.isoformat()

        stale_after: None | str | Unset
        if isinstance(self.stale_after, Unset):
            stale_after = UNSET
        elif isinstance(self.stale_after, datetime.datetime):
            stale_after = self.stale_after.isoformat()
        else:
            stale_after = self.stale_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
                "component": component,
                "rawValue": raw_value,
            }
        )
        if raw_unit is not UNSET:
            field_dict["rawUnit"] = raw_unit
        if effective_from is not UNSET:
            field_dict["effectiveFrom"] = effective_from
        if stale_after is not UNSET:
            field_dict["staleAfter"] = stale_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        component = CostComponent(d.pop("component"))

        raw_value = d.pop("rawValue")

        raw_unit = d.pop("rawUnit", UNSET)

        _effective_from = d.pop("effectiveFrom", UNSET)
        effective_from: datetime.datetime | Unset
        if isinstance(_effective_from, Unset):
            effective_from = UNSET
        else:
            effective_from = datetime.datetime.fromisoformat(_effective_from)

        def _parse_stale_after(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                stale_after_type_0 = datetime.datetime.fromisoformat(data)

                return stale_after_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        stale_after = _parse_stale_after(d.pop("staleAfter", UNSET))

        single_cost_entry_request = cls(
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
            component=component,
            raw_value=raw_value,
            raw_unit=raw_unit,
            effective_from=effective_from,
            stale_after=stale_after,
        )

        return single_cost_entry_request

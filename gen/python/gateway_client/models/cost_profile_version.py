from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.cost_component import CostComponent
from ..models.cost_profile_version_source import CostProfileVersionSource
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="CostProfileVersion")


@_attrs_define
class CostProfileVersion:
    """One append-only, effective-dated cost-profile version for a component (CST-002). The exact version in force at a
    time reproduces a historical number.

        Attributes:
            id (UUID):
            marketplace_account_id (UUID):
            variant_id (UUID):
            component (CostComponent): A cost component of the §9.2 contribution model. The set is closed. COGS and
                commission are always required; fulfillment/shipping/promotion are required when applicable to the listing;
                packaging/ads/returns are optional in P0 (an account policy may still require them).
            version (int): Monotonic version per (variant, component).
            amount (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
                mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            effective_from (datetime.datetime):
            source (CostProfileVersionSource):
            raw_text (str | Unset):
            raw_value (str | Unset):
            raw_unit (str | Unset):
            stale_after (datetime.datetime | None | Unset):
    """

    id: UUID
    marketplace_account_id: UUID
    variant_id: UUID
    component: CostComponent
    version: int
    amount: MoneyAmount
    effective_from: datetime.datetime
    source: CostProfileVersionSource
    raw_text: str | Unset = UNSET
    raw_value: str | Unset = UNSET
    raw_unit: str | Unset = UNSET
    stale_after: datetime.datetime | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        component = self.component.value

        version = self.version

        amount = self.amount.to_dict()

        effective_from = self.effective_from.isoformat()

        source = self.source.value

        raw_text = self.raw_text

        raw_value = self.raw_value

        raw_unit = self.raw_unit

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
                "id": id,
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
                "component": component,
                "version": version,
                "amount": amount,
                "effectiveFrom": effective_from,
                "source": source,
            }
        )
        if raw_text is not UNSET:
            field_dict["rawText"] = raw_text
        if raw_value is not UNSET:
            field_dict["rawValue"] = raw_value
        if raw_unit is not UNSET:
            field_dict["rawUnit"] = raw_unit
        if stale_after is not UNSET:
            field_dict["staleAfter"] = stale_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        component = CostComponent(d.pop("component"))

        version = d.pop("version")

        amount = MoneyAmount.from_dict(d.pop("amount"))

        effective_from = datetime.datetime.fromisoformat(d.pop("effectiveFrom"))

        source = CostProfileVersionSource(d.pop("source"))

        raw_text = d.pop("rawText", UNSET)

        raw_value = d.pop("rawValue", UNSET)

        raw_unit = d.pop("rawUnit", UNSET)

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

        cost_profile_version = cls(
            id=id,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
            component=component,
            version=version,
            amount=amount,
            effective_from=effective_from,
            source=source,
            raw_text=raw_text,
            raw_value=raw_value,
            raw_unit=raw_unit,
            stale_after=stale_after,
        )

        return cost_profile_version

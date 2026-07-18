from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.observation_target_tier import ObservationTargetTier

T = TypeVar("T", bound="ObservationTarget")


@_attrs_define
class ObservationTarget:
    """An observation target (PRD §7.3 OBS-001): the executable observation unit for one variant, existing ONLY for an
    active Confirmed identity.

        Attributes:
            id (UUID):
            marketplace_account_id (UUID):
            identity_id (UUID): The active Confirmed Market Product Identity being observed.
            variant_id (UUID):
            native_variant_id (int):
            native_product_id (int):
            tier (ObservationTargetTier): Cadence/freshness tier (priority 60 min, standard 6 h, background 24 h).
            cadence_seconds (int): Observation cadence for the tier, in seconds.
            freshness_deadline_seconds (int): Freshness window for the tier, in seconds.
            active (bool):
    """

    id: UUID
    marketplace_account_id: UUID
    identity_id: UUID
    variant_id: UUID
    native_variant_id: int
    native_product_id: int
    tier: ObservationTargetTier
    cadence_seconds: int
    freshness_deadline_seconds: int
    active: bool

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        marketplace_account_id = str(self.marketplace_account_id)

        identity_id = str(self.identity_id)

        variant_id = str(self.variant_id)

        native_variant_id = self.native_variant_id

        native_product_id = self.native_product_id

        tier = self.tier.value

        cadence_seconds = self.cadence_seconds

        freshness_deadline_seconds = self.freshness_deadline_seconds

        active = self.active

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "marketplaceAccountId": marketplace_account_id,
                "identityId": identity_id,
                "variantId": variant_id,
                "nativeVariantId": native_variant_id,
                "nativeProductId": native_product_id,
                "tier": tier,
                "cadenceSeconds": cadence_seconds,
                "freshnessDeadlineSeconds": freshness_deadline_seconds,
                "active": active,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        identity_id = UUID(d.pop("identityId"))

        variant_id = UUID(d.pop("variantId"))

        native_variant_id = d.pop("nativeVariantId")

        native_product_id = d.pop("nativeProductId")

        tier = ObservationTargetTier(d.pop("tier"))

        cadence_seconds = d.pop("cadenceSeconds")

        freshness_deadline_seconds = d.pop("freshnessDeadlineSeconds")

        active = d.pop("active")

        observation_target = cls(
            id=id,
            marketplace_account_id=marketplace_account_id,
            identity_id=identity_id,
            variant_id=variant_id,
            native_variant_id=native_variant_id,
            native_product_id=native_product_id,
            tier=tier,
            cadence_seconds=cadence_seconds,
            freshness_deadline_seconds=freshness_deadline_seconds,
            active=active,
        )

        return observation_target

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.cost_component import CostComponent
from ..models.margin_readiness_state import MarginReadinessState

T = TypeVar("T", bound="MarginReadiness")


@_attrs_define
class MarginReadiness:
    """Derived margin readiness for a SKU (CST-003). Only `complete` drives an executable recommendation.

    Attributes:
        variant_id (UUID):
        marketplace_account_id (UUID):
        state (MarginReadinessState): The four closed margin-readiness states (CST-003). Only `complete` may drive an
            executable recommendation; `partial` may show analysis but no approval control; `stale` and `missing` block.
        missing_components (list[CostComponent]):
        stale_components (list[CostComponent]):
        computed_at (datetime.datetime):
    """

    variant_id: UUID
    marketplace_account_id: UUID
    state: MarginReadinessState
    missing_components: list[CostComponent]
    stale_components: list[CostComponent]
    computed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        marketplace_account_id = str(self.marketplace_account_id)

        state = self.state.value

        missing_components = []
        for missing_components_item_data in self.missing_components:
            missing_components_item = missing_components_item_data.value
            missing_components.append(missing_components_item)

        stale_components = []
        for stale_components_item_data in self.stale_components:
            stale_components_item = stale_components_item_data.value
            stale_components.append(stale_components_item)

        computed_at = self.computed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "marketplaceAccountId": marketplace_account_id,
                "state": state,
                "missingComponents": missing_components,
                "staleComponents": stale_components,
                "computedAt": computed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        state = MarginReadinessState(d.pop("state"))

        missing_components = []
        _missing_components = d.pop("missingComponents")
        for missing_components_item_data in _missing_components:
            missing_components_item = CostComponent(missing_components_item_data)

            missing_components.append(missing_components_item)

        stale_components = []
        _stale_components = d.pop("staleComponents")
        for stale_components_item_data in _stale_components:
            stale_components_item = CostComponent(stale_components_item_data)

            stale_components.append(stale_components_item)

        computed_at = datetime.datetime.fromisoformat(d.pop("computedAt"))

        margin_readiness = cls(
            variant_id=variant_id,
            marketplace_account_id=marketplace_account_id,
            state=state,
            missing_components=missing_components,
            stale_components=stale_components,
            computed_at=computed_at,
        )

        return margin_readiness

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.listing_diagnostic import ListingDiagnostic


T = TypeVar("T", bound="ListingDiagnosticsReport")


@_attrs_define
class ListingDiagnosticsReport:
    """The READ-ONLY listing/image diagnostics report for one variant (LST-001). Every item names its entity + field + rule
    and carries a pass/warn result; the report NEVER generates or publishes content.

        Attributes:
            variant_id (UUID):
            marketplace_account_id (UUID):
            evaluated_at (datetime.datetime): Server time the read-only report was computed (a read, not a content edit).
            items (list[ListingDiagnostic]):
    """

    variant_id: UUID
    marketplace_account_id: UUID
    evaluated_at: datetime.datetime
    items: list[ListingDiagnostic]

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        marketplace_account_id = str(self.marketplace_account_id)

        evaluated_at = self.evaluated_at.isoformat()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "marketplaceAccountId": marketplace_account_id,
                "evaluatedAt": evaluated_at,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.listing_diagnostic import ListingDiagnostic

        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluatedAt"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ListingDiagnostic.from_dict(items_item_data)

            items.append(items_item)

        listing_diagnostics_report = cls(
            variant_id=variant_id,
            marketplace_account_id=marketplace_account_id,
            evaluated_at=evaluated_at,
            items=items,
        )

        return listing_diagnostics_report

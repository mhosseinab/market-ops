from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="WatchlistEntry")


@_attrs_define
class WatchlistEntry:
    """One EXT-007 priority-watchlist entry.

    Attributes:
        id (UUID):
        marketplace_account_id (UUID):
        variant_id (UUID):
        created_at (datetime.datetime):
    """

    id: UUID
    marketplace_account_id: UUID
    variant_id: UUID
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
                "createdAt": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        created_at = datetime.datetime.fromisoformat(d.pop("createdAt"))

        watchlist_entry = cls(
            id=id,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
            created_at=created_at,
        )

        return watchlist_entry

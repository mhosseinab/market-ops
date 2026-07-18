from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="WatchlistAddRequest")


@_attrs_define
class WatchlistAddRequest:
    """
    Attributes:
        marketplace_account_id (UUID):
        variant_id (UUID): MUST be a Confirmed owned product (CAT-002); otherwise rejected.
    """

    marketplace_account_id: UUID
    variant_id: UUID

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        watchlist_add_request = cls(
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
        )

        return watchlist_add_request

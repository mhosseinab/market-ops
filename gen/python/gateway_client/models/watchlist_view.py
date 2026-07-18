from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.watchlist_entry import WatchlistEntry


T = TypeVar("T", bound="WatchlistView")


@_attrs_define
class WatchlistView:
    """
    Attributes:
        marketplace_account_id (UUID):
        cap (int): The server-enforced maximum watchlist size (EXT-007).
        items (list[WatchlistEntry]):
    """

    marketplace_account_id: UUID
    cap: int
    items: list[WatchlistEntry]

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        cap = self.cap

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "cap": cap,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.watchlist_entry import WatchlistEntry

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        cap = d.pop("cap")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = WatchlistEntry.from_dict(items_item_data)

            items.append(items_item)

        watchlist_view = cls(
            marketplace_account_id=marketplace_account_id,
            cap=cap,
            items=items,
        )

        return watchlist_view

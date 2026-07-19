from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.catalog_product_row import CatalogProductRow


T = TypeVar("T", bound="CatalogProductPage")


@_attrs_define
class CatalogProductPage:
    """One page of canonical Product rows, ordered by native_variant_id ascending.

    Attributes:
        items (list[CatalogProductRow]):
        next_cursor (None | str | Unset): Cursor for the next page (native_variant_id of the last row); null at end.
    """

    items: list[CatalogProductRow]
    next_cursor: None | str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_cursor: None | str | Unset
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
            }
        )
        if next_cursor is not UNSET:
            field_dict["nextCursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.catalog_product_row import CatalogProductRow

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = CatalogProductRow.from_dict(items_item_data)

            items.append(items_item)

        def _parse_next_cursor(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        next_cursor = _parse_next_cursor(d.pop("nextCursor", UNSET))

        catalog_product_page = cls(
            items=items,
            next_cursor=next_cursor,
        )

        return catalog_product_page

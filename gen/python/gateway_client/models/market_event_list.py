from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.market_event import MarketEvent


T = TypeVar("T", bound="MarketEventList")


@_attrs_define
class MarketEventList:
    """
    Attributes:
        items (list[MarketEvent]):
    """

    items: list[MarketEvent]

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.market_event import MarketEvent

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = MarketEvent.from_dict(items_item_data)

            items.append(items_item)

        market_event_list = cls(
            items=items,
        )

        return market_event_list

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.needs_review_item import NeedsReviewItem


T = TypeVar("T", bound="NeedsReviewQueue")


@_attrs_define
class NeedsReviewQueue:
    """The account's pending Needs Review identity-mapping candidates.

    Attributes:
        items (list[NeedsReviewItem]): Pending candidates; empty when the queue is clear.
    """

    items: list[NeedsReviewItem]

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
        from ..models.needs_review_item import NeedsReviewItem

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = NeedsReviewItem.from_dict(items_item_data)

            items.append(items_item)

        needs_review_queue = cls(
            items=items,
        )

        return needs_review_queue

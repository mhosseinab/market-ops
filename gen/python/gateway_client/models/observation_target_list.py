from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.observation_target import ObservationTarget


T = TypeVar("T", bound="ObservationTargetList")


@_attrs_define
class ObservationTargetList:
    """The account's active observation targets.

    Attributes:
        items (list[ObservationTarget]):
    """

    items: list[ObservationTarget]

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
        from ..models.observation_target import ObservationTarget

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObservationTarget.from_dict(items_item_data)

            items.append(items_item)

        observation_target_list = cls(
            items=items,
        )

        return observation_target_list

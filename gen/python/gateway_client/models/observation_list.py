from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.observation import Observation


T = TypeVar("T", bound="ObservationList")


@_attrs_define
class ObservationList:
    """Append-only observation evidence for a target, newest first.

    Attributes:
        items (list[Observation]):
    """

    items: list[Observation]

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
        from ..models.observation import Observation

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = Observation.from_dict(items_item_data)

            items.append(items_item)

        observation_list = cls(
            items=items,
        )

        return observation_list

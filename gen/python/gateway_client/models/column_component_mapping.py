from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.cost_component import CostComponent

T = TypeVar("T", bound="ColumnComponentMapping")


@_attrs_define
class ColumnComponentMapping:
    """Maps one CSV header column to the cost component it supplies.

    Attributes:
        header (str): The CSV header text of the column.
        component (CostComponent): A cost component of the §9.2 contribution model. The set is closed. COGS and
            commission are always required; fulfillment/shipping/promotion are required when applicable to the listing;
            packaging/ads/returns are optional in P0 (an account policy may still require them).
    """

    header: str
    component: CostComponent

    def to_dict(self) -> dict[str, Any]:
        header = self.header

        component = self.component.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "header": header,
                "component": component,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        header = d.pop("header")

        component = CostComponent(d.pop("component"))

        column_component_mapping = cls(
            header=header,
            component=component,
        )

        return column_component_mapping

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.column_component_mapping import ColumnComponentMapping


T = TypeVar("T", bound="DetectedMapping")


@_attrs_define
class DetectedMapping:
    """How the import interpreted the CSV columns, echoed for the seller to confirm the mapping (CST-001 mapping preview).

    Attributes:
        sku_column (str): The header text detected/used as the SKU column.
        component_columns (list[ColumnComponentMapping]):
    """

    sku_column: str
    component_columns: list[ColumnComponentMapping]

    def to_dict(self) -> dict[str, Any]:
        sku_column = self.sku_column

        component_columns = []
        for component_columns_item_data in self.component_columns:
            component_columns_item = component_columns_item_data.to_dict()
            component_columns.append(component_columns_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "skuColumn": sku_column,
                "componentColumns": component_columns,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.column_component_mapping import ColumnComponentMapping

        d = dict(src_dict)
        sku_column = d.pop("skuColumn")

        component_columns = []
        _component_columns = d.pop("componentColumns")
        for component_columns_item_data in _component_columns:
            component_columns_item = ColumnComponentMapping.from_dict(component_columns_item_data)

            component_columns.append(component_columns_item)

        detected_mapping = cls(
            sku_column=sku_column,
            component_columns=component_columns,
        )

        return detected_mapping

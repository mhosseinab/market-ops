from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.column_component_mapping import ColumnComponentMapping


T = TypeVar("T", bound="CostImportPreviewRequest")


@_attrs_define
class CostImportPreviewRequest:
    """Request to build a CSV cost-import preview. `csv` is the UTF-8 file content. An explicit column mapping is optional;
    when omitted the columns are auto-detected from the header row.

        Attributes:
            marketplace_account_id (UUID): The account the costs belong to.
            csv (str): The UTF-8 CSV content.
            filename (str | Unset): Original file name, for the audit/preview display.
            sku_column (str | Unset): Explicit SKU column header (optional; auto-detected when omitted).
            component_columns (list[ColumnComponentMapping] | Unset): Explicit component column mapping (optional; auto-
                detected when omitted).
    """

    marketplace_account_id: UUID
    csv: str
    filename: str | Unset = UNSET
    sku_column: str | Unset = UNSET
    component_columns: list[ColumnComponentMapping] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        csv = self.csv

        filename = self.filename

        sku_column = self.sku_column

        component_columns: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.component_columns, Unset):
            component_columns = []
            for component_columns_item_data in self.component_columns:
                component_columns_item = component_columns_item_data.to_dict()
                component_columns.append(component_columns_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "csv": csv,
            }
        )
        if filename is not UNSET:
            field_dict["filename"] = filename
        if sku_column is not UNSET:
            field_dict["skuColumn"] = sku_column
        if component_columns is not UNSET:
            field_dict["componentColumns"] = component_columns

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.column_component_mapping import ColumnComponentMapping

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        csv = d.pop("csv")

        filename = d.pop("filename", UNSET)

        sku_column = d.pop("skuColumn", UNSET)

        _component_columns = d.pop("componentColumns", UNSET)
        component_columns: list[ColumnComponentMapping] | Unset = UNSET
        if _component_columns is not UNSET:
            component_columns = []
            for component_columns_item_data in _component_columns:
                component_columns_item = ColumnComponentMapping.from_dict(component_columns_item_data)

                component_columns.append(component_columns_item)

        cost_import_preview_request = cls(
            marketplace_account_id=marketplace_account_id,
            csv=csv,
            filename=filename,
            sku_column=sku_column,
            component_columns=component_columns,
        )

        return cost_import_preview_request

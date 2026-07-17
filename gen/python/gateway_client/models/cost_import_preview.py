from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.cost_import_preview_status import CostImportPreviewStatus
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.cost_import_counts import CostImportCounts
    from ..models.cost_import_row import CostImportRow
    from ..models.detected_mapping import DetectedMapping


T = TypeVar("T", bound="CostImportPreview")


@_attrs_define
class CostImportPreview:
    """A preview batch (status 'preview') with its per-row dispositions.

    Attributes:
        batch_id (UUID):
        marketplace_account_id (UUID):
        status (CostImportPreviewStatus):
        counts (CostImportCounts): Disposition tally backing the preview cards and the commit guard.
        rows (list[CostImportRow]):
        filename (str | Unset):
        detected (DetectedMapping | Unset): How the import interpreted the CSV columns, echoed for the seller to confirm
            the mapping (CST-001 mapping preview).
    """

    batch_id: UUID
    marketplace_account_id: UUID
    status: CostImportPreviewStatus
    counts: CostImportCounts
    rows: list[CostImportRow]
    filename: str | Unset = UNSET
    detected: DetectedMapping | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        batch_id = str(self.batch_id)

        marketplace_account_id = str(self.marketplace_account_id)

        status = self.status.value

        counts = self.counts.to_dict()

        rows = []
        for rows_item_data in self.rows:
            rows_item = rows_item_data.to_dict()
            rows.append(rows_item)

        filename = self.filename

        detected: dict[str, Any] | Unset = UNSET
        if not isinstance(self.detected, Unset):
            detected = self.detected.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "batchId": batch_id,
                "marketplaceAccountId": marketplace_account_id,
                "status": status,
                "counts": counts,
                "rows": rows,
            }
        )
        if filename is not UNSET:
            field_dict["filename"] = filename
        if detected is not UNSET:
            field_dict["detected"] = detected

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.cost_import_counts import CostImportCounts
        from ..models.cost_import_row import CostImportRow
        from ..models.detected_mapping import DetectedMapping

        d = dict(src_dict)
        batch_id = UUID(d.pop("batchId"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        status = CostImportPreviewStatus(d.pop("status"))

        counts = CostImportCounts.from_dict(d.pop("counts"))

        rows = []
        _rows = d.pop("rows")
        for rows_item_data in _rows:
            rows_item = CostImportRow.from_dict(rows_item_data)

            rows.append(rows_item)

        filename = d.pop("filename", UNSET)

        _detected = d.pop("detected", UNSET)
        detected: DetectedMapping | Unset
        if isinstance(_detected, Unset):
            detected = UNSET
        else:
            detected = DetectedMapping.from_dict(_detected)

        cost_import_preview = cls(
            batch_id=batch_id,
            marketplace_account_id=marketplace_account_id,
            status=status,
            counts=counts,
            rows=rows,
            filename=filename,
            detected=detected,
        )

        return cost_import_preview

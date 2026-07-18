from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.cost_import_commit_result_status import CostImportCommitResultStatus

T = TypeVar("T", bound="CostImportCommitResult")


@_attrs_define
class CostImportCommitResult:
    """Result of committing a preview batch.

    Attributes:
        batch_id (UUID):
        status (CostImportCommitResultStatus):
        committed_rows (int): Number of accepted rows committed as cost-profile versions.
        affected_variant_ids (list[UUID]):
    """

    batch_id: UUID
    status: CostImportCommitResultStatus
    committed_rows: int
    affected_variant_ids: list[UUID]

    def to_dict(self) -> dict[str, Any]:
        batch_id = str(self.batch_id)

        status = self.status.value

        committed_rows = self.committed_rows

        affected_variant_ids = []
        for affected_variant_ids_item_data in self.affected_variant_ids:
            affected_variant_ids_item = str(affected_variant_ids_item_data)
            affected_variant_ids.append(affected_variant_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "batchId": batch_id,
                "status": status,
                "committedRows": committed_rows,
                "affectedVariantIds": affected_variant_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        batch_id = UUID(d.pop("batchId"))

        status = CostImportCommitResultStatus(d.pop("status"))

        committed_rows = d.pop("committedRows")

        affected_variant_ids = []
        _affected_variant_ids = d.pop("affectedVariantIds")
        for affected_variant_ids_item_data in _affected_variant_ids:
            affected_variant_ids_item = UUID(affected_variant_ids_item_data)

            affected_variant_ids.append(affected_variant_ids_item)

        cost_import_commit_result = cls(
            batch_id=batch_id,
            status=status,
            committed_rows=committed_rows,
            affected_variant_ids=affected_variant_ids,
        )

        return cost_import_commit_result

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CostImportCommitRequest")


@_attrs_define
class CostImportCommitRequest:
    """Confirm and commit a preview batch (CST-001).

    Attributes:
        batch_id (UUID):
    """

    batch_id: UUID

    def to_dict(self) -> dict[str, Any]:
        batch_id = str(self.batch_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "batchId": batch_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        batch_id = UUID(d.pop("batchId"))

        cost_import_commit_request = cls(
            batch_id=batch_id,
        )

        return cost_import_commit_request

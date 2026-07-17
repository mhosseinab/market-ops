from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="BulkApprovalConfirmResult")


@_attrs_define
class BulkApprovalConfirmResult:
    """The outcome of a bulk confirmation. `valid` is false when the bound selection-set version is stale (invalidated by a
    set/evidence change). `executionPending` is true for a valid bulk confirmation — per-item execution lands in S18.

        Attributes:
            selection_set_lineage (UUID):
            bound_version (int):
            valid (bool):
            execution_pending (bool):
            current_version (int | Unset): The current selection-set version (differs from bound when stale).
    """

    selection_set_lineage: UUID
    bound_version: int
    valid: bool
    execution_pending: bool
    current_version: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        selection_set_lineage = str(self.selection_set_lineage)

        bound_version = self.bound_version

        valid = self.valid

        execution_pending = self.execution_pending

        current_version = self.current_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "selectionSetLineage": selection_set_lineage,
                "boundVersion": bound_version,
                "valid": valid,
                "executionPending": execution_pending,
            }
        )
        if current_version is not UNSET:
            field_dict["currentVersion"] = current_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        selection_set_lineage = UUID(d.pop("selectionSetLineage"))

        bound_version = d.pop("boundVersion")

        valid = d.pop("valid")

        execution_pending = d.pop("executionPending")

        current_version = d.pop("currentVersion", UNSET)

        bulk_approval_confirm_result = cls(
            selection_set_lineage=selection_set_lineage,
            bound_version=bound_version,
            valid=valid,
            execution_pending=execution_pending,
            current_version=current_version,
        )

        return bulk_approval_confirm_result

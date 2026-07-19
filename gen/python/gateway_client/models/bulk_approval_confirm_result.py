from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.bulk_approval_item_result import BulkApprovalItemResult


T = TypeVar("T", bound="BulkApprovalConfirmResult")


@_attrs_define
class BulkApprovalConfirmResult:
    """The AUTHORITATIVE outcome of a bulk confirmation (issue #90). `valid` is false when the bound selection-set version
    is stale (invalidated by a set/evidence change), in which case NOTHING is authorized and `items` is empty. When
    `valid`, each executable member is durably authorized through the same §8.4 individual-confirm path and reported in
    `items` with an explicit per-item state; blocked/warning members are `excluded` and never execute.
    `executionPending` is true only when at least one member now carries a durable, pending execution authorization.

        Attributes:
            selection_set_lineage (UUID):
            bound_version (int):
            valid (bool):
            execution_pending (bool):
            items (list[BulkApprovalItemResult]): One durable result per member of the bound version. Empty when the
                confirmation is invalid (nothing authorized).
            current_version (int | Unset): The current selection-set version (differs from bound when stale).
    """

    selection_set_lineage: UUID
    bound_version: int
    valid: bool
    execution_pending: bool
    items: list[BulkApprovalItemResult]
    current_version: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        selection_set_lineage = str(self.selection_set_lineage)

        bound_version = self.bound_version

        valid = self.valid

        execution_pending = self.execution_pending

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        current_version = self.current_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "selectionSetLineage": selection_set_lineage,
                "boundVersion": bound_version,
                "valid": valid,
                "executionPending": execution_pending,
                "items": items,
            }
        )
        if current_version is not UNSET:
            field_dict["currentVersion"] = current_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.bulk_approval_item_result import BulkApprovalItemResult

        d = dict(src_dict)
        selection_set_lineage = UUID(d.pop("selectionSetLineage"))

        bound_version = d.pop("boundVersion")

        valid = d.pop("valid")

        execution_pending = d.pop("executionPending")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = BulkApprovalItemResult.from_dict(items_item_data)

            items.append(items_item)

        current_version = d.pop("currentVersion", UNSET)

        bulk_approval_confirm_result = cls(
            selection_set_lineage=selection_set_lineage,
            bound_version=bound_version,
            valid=valid,
            execution_pending=execution_pending,
            items=items,
            current_version=current_version,
        )

        return bulk_approval_confirm_result

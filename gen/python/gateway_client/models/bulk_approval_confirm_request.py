from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="BulkApprovalConfirmRequest")


@_attrs_define
class BulkApprovalConfirmRequest:
    """A bulk approval confirmation bound to ONE exact selection-set version (CHAT-052). The server rejects it when the
    bound version is no longer current (any set/evidence change mints a new version).

        Attributes:
            selection_set_lineage (UUID): The selection-set lineage the preview was built from.
            bound_version (int): The exact selection-set version the preview bound to.
    """

    selection_set_lineage: UUID
    bound_version: int

    def to_dict(self) -> dict[str, Any]:
        selection_set_lineage = str(self.selection_set_lineage)

        bound_version = self.bound_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "selectionSetLineage": selection_set_lineage,
                "boundVersion": bound_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        selection_set_lineage = UUID(d.pop("selectionSetLineage"))

        bound_version = d.pop("boundVersion")

        bulk_approval_confirm_request = cls(
            selection_set_lineage=selection_set_lineage,
            bound_version=bound_version,
        )

        return bulk_approval_confirm_request

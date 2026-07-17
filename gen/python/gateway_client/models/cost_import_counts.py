from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CostImportCounts")


@_attrs_define
class CostImportCounts:
    """Disposition tally backing the preview cards and the commit guard.

    Attributes:
        accept (int):
        reject (int):
        duplicate (int):
    """

    accept: int
    reject: int
    duplicate: int

    def to_dict(self) -> dict[str, Any]:
        accept = self.accept

        reject = self.reject

        duplicate = self.duplicate

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "accept": accept,
                "reject": reject,
                "duplicate": duplicate,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        accept = d.pop("accept")

        reject = d.pop("reject")

        duplicate = d.pop("duplicate")

        cost_import_counts = cls(
            accept=accept,
            reject=reject,
            duplicate=duplicate,
        )

        return cost_import_counts

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SelectionSetPreviewMemberInput")


@_attrs_define
class SelectionSetPreviewMemberInput:
    """One candidate member for a bulk selection-set preview. The server resolves the disposition from the NAMED
    recommendation's own persisted state (approvable / blockers) — never from a client assertion.

        Attributes:
            variant_id (UUID):
            recommendation_id (UUID):
    """

    variant_id: UUID
    recommendation_id: UUID

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        recommendation_id = str(self.recommendation_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "recommendationId": recommendation_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        recommendation_id = UUID(d.pop("recommendationId"))

        selection_set_preview_member_input = cls(
            variant_id=variant_id,
            recommendation_id=recommendation_id,
        )

        return selection_set_preview_member_input

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.selection_set_disposition import SelectionSetDisposition

T = TypeVar("T", bound="SelectionSetMemberView")


@_attrs_define
class SelectionSetMemberView:
    """
    Attributes:
        variant_id (UUID):
        recommendation_id (UUID):
        disposition (SelectionSetDisposition): A selection-set member's bulk disposition (CHAT-050).
    """

    variant_id: UUID
    recommendation_id: UUID
    disposition: SelectionSetDisposition

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        recommendation_id = str(self.recommendation_id)

        disposition = self.disposition.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "recommendationId": recommendation_id,
                "disposition": disposition,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        recommendation_id = UUID(d.pop("recommendationId"))

        disposition = SelectionSetDisposition(d.pop("disposition"))

        selection_set_member_view = cls(
            variant_id=variant_id,
            recommendation_id=recommendation_id,
            disposition=disposition,
        )

        return selection_set_member_view

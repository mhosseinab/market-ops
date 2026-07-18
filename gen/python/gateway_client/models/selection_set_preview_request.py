from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.selection_set_preview_member_input import SelectionSetPreviewMemberInput
    from ..models.selection_set_preview_request_criteria import SelectionSetPreviewRequestCriteria


T = TypeVar("T", bound="SelectionSetPreviewRequest")


@_attrs_define
class SelectionSetPreviewRequest:
    """The screens-native bulk preview request (PD-3 item 4). Carries NO version field by construction — the server is the
    sole authority that mints the selection-set version.

        Attributes:
            marketplace_account_id (UUID):
            name (str):
            members (list[SelectionSetPreviewMemberInput]):
            lineage_id (UUID | Unset): Existing selection-set lineage to mint the NEXT version within (a refreshed preview).
                Omit to start a brand-new lineage.
            criteria (SelectionSetPreviewRequestCriteria | Unset): Deterministic query criteria the set was compiled from.
    """

    marketplace_account_id: UUID
    name: str
    members: list[SelectionSetPreviewMemberInput]
    lineage_id: UUID | Unset = UNSET
    criteria: SelectionSetPreviewRequestCriteria | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        name = self.name

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        lineage_id: str | Unset = UNSET
        if not isinstance(self.lineage_id, Unset):
            lineage_id = str(self.lineage_id)

        criteria: dict[str, Any] | Unset = UNSET
        if not isinstance(self.criteria, Unset):
            criteria = self.criteria.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "name": name,
                "members": members,
            }
        )
        if lineage_id is not UNSET:
            field_dict["lineageId"] = lineage_id
        if criteria is not UNSET:
            field_dict["criteria"] = criteria

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.selection_set_preview_member_input import SelectionSetPreviewMemberInput
        from ..models.selection_set_preview_request_criteria import SelectionSetPreviewRequestCriteria

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        name = d.pop("name")

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = SelectionSetPreviewMemberInput.from_dict(members_item_data)

            members.append(members_item)

        _lineage_id = d.pop("lineageId", UNSET)
        lineage_id: UUID | Unset
        if isinstance(_lineage_id, Unset):
            lineage_id = UNSET
        else:
            lineage_id = UUID(_lineage_id)

        _criteria = d.pop("criteria", UNSET)
        criteria: SelectionSetPreviewRequestCriteria | Unset
        if isinstance(_criteria, Unset):
            criteria = UNSET
        else:
            criteria = SelectionSetPreviewRequestCriteria.from_dict(_criteria)

        selection_set_preview_request = cls(
            marketplace_account_id=marketplace_account_id,
            name=name,
            members=members,
            lineage_id=lineage_id,
            criteria=criteria,
        )

        return selection_set_preview_request

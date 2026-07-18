from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_exposure import EventExposure
    from ..models.selection_set_member_view import SelectionSetMemberView


T = TypeVar("T", bound="SelectionSetPreviewResult")


@_attrs_define
class SelectionSetPreviewResult:
    """The server-minted selection-set preview (PD-3 item 4). `version` is assigned ENTIRELY server-side (append-only "next
    version per lineage"); a subsequent bulk confirmation (POST /approvals/bulk/confirm) binds to EXACTLY this lineage +
    version.

        Attributes:
            id (UUID):
            lineage_id (UUID):
            version (int):
            name (str):
            member_count (int):
            members (list[SelectionSetMemberView]):
            aggregate_impact (EventExposure | Unset): An event's business impact (PRD §7.4 EVT-004/EVT-005). It is EITHER a
                known Money amount (derived from margin/sales context) OR explicitly unknown. When `known` is false there is NO
                `amount` at all — a missing sales/cost context is never a fabricated number (EVT-005).
    """

    id: UUID
    lineage_id: UUID
    version: int
    name: str
    member_count: int
    members: list[SelectionSetMemberView]
    aggregate_impact: EventExposure | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        lineage_id = str(self.lineage_id)

        version = self.version

        name = self.name

        member_count = self.member_count

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        aggregate_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.aggregate_impact, Unset):
            aggregate_impact = self.aggregate_impact.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "lineageId": lineage_id,
                "version": version,
                "name": name,
                "memberCount": member_count,
                "members": members,
            }
        )
        if aggregate_impact is not UNSET:
            field_dict["aggregateImpact"] = aggregate_impact

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_exposure import EventExposure
        from ..models.selection_set_member_view import SelectionSetMemberView

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        lineage_id = UUID(d.pop("lineageId"))

        version = d.pop("version")

        name = d.pop("name")

        member_count = d.pop("memberCount")

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = SelectionSetMemberView.from_dict(members_item_data)

            members.append(members_item)

        _aggregate_impact = d.pop("aggregateImpact", UNSET)
        aggregate_impact: EventExposure | Unset
        if isinstance(_aggregate_impact, Unset):
            aggregate_impact = UNSET
        else:
            aggregate_impact = EventExposure.from_dict(_aggregate_impact)

        selection_set_preview_result = cls(
            id=id,
            lineage_id=lineage_id,
            version=version,
            name=name,
            member_count=member_count,
            members=members,
            aggregate_impact=aggregate_impact,
        )

        return selection_set_preview_result

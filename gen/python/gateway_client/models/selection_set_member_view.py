from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.selection_set_disposition import SelectionSetDisposition
from ..types import UNSET, Unset

T = TypeVar("T", bound="SelectionSetMemberView")


@_attrs_define
class SelectionSetMemberView:
    """One resolved member of a selection-set preview, with its SERVER-derived disposition and SERVER-SEALED offer identity
    (issue #87).

        Attributes:
            variant_id (UUID):
            recommendation_id (UUID):
            disposition (SelectionSetDisposition): A selection-set member's bulk disposition (CHAT-050).
            offer_identity (str | Unset): The observed-offer identity the SERVER sealed onto this member, from the member's
                OWN recommendation evidence (OBS-004). It is never derived by a lookup-by-target: a target may carry MANY offer
                identities, and picking one by target is the #87 defect itself. The authoritative bulk confirmation reports this
                SAME value, so preview and execution retain one explicit identity.
                An empty string is EXPLICIT ABSENCE — a recommendation that is not observation-driven, or a selection-set
                version sealed before #87 — never a stand-in for some other offer. ADDITIVE and OPTIONAL: it is not in
                `required`, so a client generated before #87 is unaffected.
    """

    variant_id: UUID
    recommendation_id: UUID
    disposition: SelectionSetDisposition
    offer_identity: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        recommendation_id = str(self.recommendation_id)

        disposition = self.disposition.value

        offer_identity = self.offer_identity

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "recommendationId": recommendation_id,
                "disposition": disposition,
            }
        )
        if offer_identity is not UNSET:
            field_dict["offerIdentity"] = offer_identity

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        recommendation_id = UUID(d.pop("recommendationId"))

        disposition = SelectionSetDisposition(d.pop("disposition"))

        offer_identity = d.pop("offerIdentity", UNSET)

        selection_set_member_view = cls(
            variant_id=variant_id,
            recommendation_id=recommendation_id,
            disposition=disposition,
            offer_identity=offer_identity,
        )

        return selection_set_member_view

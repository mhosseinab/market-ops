from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SelectionSetPreviewMemberInput")


@_attrs_define
class SelectionSetPreviewMemberInput:
    """One candidate member for a bulk selection-set preview. The server resolves the disposition from the NAMED
    recommendation's own persisted state (approvable / blockers) — never from a client assertion.
    BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY (issue #87). The optional `offerIdentity` below
    is ADDITIVE: it is NOT in this schema's `required` set, `additionalProperties: false` is unchanged, and the handler
    NEVER requires it. An existing generated client submitting the formerly valid {variantId, recommendationId} shape
    keeps working unchanged and still receives the SERVER-SEALED identity back.

        Attributes:
            variant_id (UUID):
            recommendation_id (UUID):
            offer_identity (str | Unset): OPTIONAL client SELECTOR — never an assertion the server acts on. The server seals
                this member's observed-offer identity from the NAMED recommendation's OWN evidence observation; when this field
                is present it is COMPARED to that sealed value, and a mismatch fails closed as the SAME uniform not-found an
                unknown member produces (no existence oracle for the real identity). It can therefore NARROW (reject) but never
                WIDEN or REDIRECT what gets authorized. Omit it to accept the server's sealed value.
    """

    variant_id: UUID
    recommendation_id: UUID
    offer_identity: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        recommendation_id = str(self.recommendation_id)

        offer_identity = self.offer_identity

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "recommendationId": recommendation_id,
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

        offer_identity = d.pop("offerIdentity", UNSET)

        selection_set_preview_member_input = cls(
            variant_id=variant_id,
            recommendation_id=recommendation_id,
            offer_identity=offer_identity,
        )

        return selection_set_preview_member_input

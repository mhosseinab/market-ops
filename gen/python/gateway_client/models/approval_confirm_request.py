from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.approval_binding import ApprovalBinding


T = TypeVar("T", bound="ApprovalConfirmRequest")


@_attrs_define
class ApprovalConfirmRequest:
    """The structured individual-approval control activation (§8, APR-001). It MUST carry the exact bound versions; the
    server re-verifies every one against the live card. This is the only individual approval path — free text cannot
    satisfy it.

        Attributes:
            card_id (UUID):
            binding (ApprovalBinding): The APR-001 version binding of an approval control: the exact action id,
                parameter/context/policy/cost versions, evidence versions, and expiry. ANY change to a bound dimension, or a
                reached expiry, invalidates the control.
    """

    card_id: UUID
    binding: ApprovalBinding

    def to_dict(self) -> dict[str, Any]:
        card_id = str(self.card_id)

        binding = self.binding.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cardId": card_id,
                "binding": binding,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.approval_binding import ApprovalBinding

        d = dict(src_dict)
        card_id = UUID(d.pop("cardId"))

        binding = ApprovalBinding.from_dict(d.pop("binding"))

        approval_confirm_request = cls(
            card_id=card_id,
            binding=binding,
        )

        return approval_confirm_request

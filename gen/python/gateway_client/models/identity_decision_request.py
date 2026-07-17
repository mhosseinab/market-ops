from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="IdentityDecisionRequest")


@_attrs_define
class IdentityDecisionRequest:
    """A confirm / reject / defer decision on a Needs Review candidate. The optional note is free text captured as audit
    evidence; it carries NO authority (PRD §8): the structured operation itself is the decision.

        Attributes:
            identity_id (UUID): The Market Product Identity to act on.
            note (str | Unset): Optional reviewer note stored as append-only audit evidence.
    """

    identity_id: UUID
    note: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        identity_id = str(self.identity_id)

        note = self.note

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "identityId": identity_id,
            }
        )
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        identity_id = UUID(d.pop("identityId"))

        note = d.pop("note", UNSET)

        identity_decision_request = cls(
            identity_id=identity_id,
            note=note,
        )

        return identity_decision_request

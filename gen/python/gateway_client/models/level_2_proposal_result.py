from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="Level2ProposalResult")


@_attrs_define
class Level2ProposalResult:
    """The proposal's identifiers, bound versions, scope/consequence catalog keys, and expiry. The proposal + its append-
    only audit row are written in one transaction (AUD-001). Version values are opaque strings. TERMINAL AT DRAFT.

        Attributes:
            draft_id (UUID):
            action_id (UUID):
            context_version (str):
            parameter_version (str):
            expires_at (datetime.datetime):
            scope_key (str): The catalog key naming what the change affects.
            consequence_key (str): The catalog key naming the reversible consequence.
    """

    draft_id: UUID
    action_id: UUID
    context_version: str
    parameter_version: str
    expires_at: datetime.datetime
    scope_key: str
    consequence_key: str

    def to_dict(self) -> dict[str, Any]:
        draft_id = str(self.draft_id)

        action_id = str(self.action_id)

        context_version = self.context_version

        parameter_version = self.parameter_version

        expires_at = self.expires_at.isoformat()

        scope_key = self.scope_key

        consequence_key = self.consequence_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "draft_id": draft_id,
                "action_id": action_id,
                "context_version": context_version,
                "parameter_version": parameter_version,
                "expires_at": expires_at,
                "scope_key": scope_key,
                "consequence_key": consequence_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        draft_id = UUID(d.pop("draft_id"))

        action_id = UUID(d.pop("action_id"))

        context_version = d.pop("context_version")

        parameter_version = d.pop("parameter_version")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        scope_key = d.pop("scope_key")

        consequence_key = d.pop("consequence_key")

        level_2_proposal_result = cls(
            draft_id=draft_id,
            action_id=action_id,
            context_version=context_version,
            parameter_version=parameter_version,
            expires_at=expires_at,
            scope_key=scope_key,
            consequence_key=consequence_key,
        )

        return level_2_proposal_result

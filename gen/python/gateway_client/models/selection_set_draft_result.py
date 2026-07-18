from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SelectionSetDraftResult")


@_attrs_define
class SelectionSetDraftResult:
    """The created selection-set Draft's identifiers, bound version, and expiry. Version values are opaque strings.
    TERMINAL AT DRAFT.

        Attributes:
            draft_id (UUID):
            action_id (UUID):
            context_version (str):
            parameter_version (str):
            expires_at (datetime.datetime):
    """

    draft_id: UUID
    action_id: UUID
    context_version: str
    parameter_version: str
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        draft_id = str(self.draft_id)

        action_id = str(self.action_id)

        context_version = self.context_version

        parameter_version = self.parameter_version

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "draft_id": draft_id,
                "action_id": action_id,
                "context_version": context_version,
                "parameter_version": parameter_version,
                "expires_at": expires_at,
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

        selection_set_draft_result = cls(
            draft_id=draft_id,
            action_id=action_id,
            context_version=context_version,
            parameter_version=parameter_version,
            expires_at=expires_at,
        )

        return selection_set_draft_result

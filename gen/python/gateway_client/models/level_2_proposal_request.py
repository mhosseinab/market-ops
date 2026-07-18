from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="Level2ProposalRequest")


@_attrs_define
class Level2ProposalRequest:
    """A Level-2 reversible-config proposal (CHAT-061/062): before/after catalog keys plus the setting being changed. Keys
    are locale-neutral catalog keys (LOC-001) — no copy in the core.

        Attributes:
            marketplace_account_id (UUID):
            setting_key (str):
            before_key (str):
            after_key (str):
    """

    marketplace_account_id: UUID
    setting_key: str
    before_key: str
    after_key: str

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        setting_key = self.setting_key

        before_key = self.before_key

        after_key = self.after_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplace_account_id": marketplace_account_id,
                "setting_key": setting_key,
                "before_key": before_key,
                "after_key": after_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplace_account_id"))

        setting_key = d.pop("setting_key")

        before_key = d.pop("before_key")

        after_key = d.pop("after_key")

        level_2_proposal_request = cls(
            marketplace_account_id=marketplace_account_id,
            setting_key=setting_key,
            before_key=before_key,
            after_key=after_key,
        )

        return level_2_proposal_request

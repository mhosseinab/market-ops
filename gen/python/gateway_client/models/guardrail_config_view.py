from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.guardrail_settings import GuardrailSettings


T = TypeVar("T", bound="GuardrailConfigView")


@_attrs_define
class GuardrailConfigView:
    """
    Attributes:
        marketplace_account_id (UUID):
        settings (GuardrailSettings): The L3 commercial guardrails (PD-3 item 6).
        version (int): Optimistic-concurrency token (issue #101). Echo this value as `expectedVersion` on the next
            setGuardrails write; a mismatch is a safe conflict (409), never a lost update. A never-configured account has no
            view (404), so a first write uses expectedVersion 0.
        updated_at (datetime.datetime):
        updated_by (str | Unset): The Owner actor id who last wrote these guardrails (AUD-001).
    """

    marketplace_account_id: UUID
    settings: GuardrailSettings
    version: int
    updated_at: datetime.datetime
    updated_by: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        settings = self.settings.to_dict()

        version = self.version

        updated_at = self.updated_at.isoformat()

        updated_by = self.updated_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "settings": settings,
                "version": version,
                "updatedAt": updated_at,
            }
        )
        if updated_by is not UNSET:
            field_dict["updatedBy"] = updated_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.guardrail_settings import GuardrailSettings

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        settings = GuardrailSettings.from_dict(d.pop("settings"))

        version = d.pop("version")

        updated_at = datetime.datetime.fromisoformat(d.pop("updatedAt"))

        updated_by = d.pop("updatedBy", UNSET)

        guardrail_config_view = cls(
            marketplace_account_id=marketplace_account_id,
            settings=settings,
            version=version,
            updated_at=updated_at,
            updated_by=updated_by,
        )

        return guardrail_config_view

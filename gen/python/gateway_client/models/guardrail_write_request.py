from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.guardrail_settings import GuardrailSettings


T = TypeVar("T", bound="GuardrailWriteRequest")


@_attrs_define
class GuardrailWriteRequest:
    """
    Attributes:
        marketplace_account_id (UUID):
        settings (GuardrailSettings): The L3 commercial guardrails (PD-3 item 6).
        expected_version (int | Unset): The version the caller last read (GuardrailConfigView.version), for optimistic
            concurrency. Omitted or 0 means "first write" (no config yet); a value that no longer matches the persisted
            version is a safe 409 conflict with NO mutation and NO audit row. Guardrails are stricter-only (PRC-004 / §8.3):
            a write may only tighten the authoritative effective baseline. Default: 0.
    """

    marketplace_account_id: UUID
    settings: GuardrailSettings
    expected_version: int | Unset = 0

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        settings = self.settings.to_dict()

        expected_version = self.expected_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "settings": settings,
            }
        )
        if expected_version is not UNSET:
            field_dict["expectedVersion"] = expected_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.guardrail_settings import GuardrailSettings

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        settings = GuardrailSettings.from_dict(d.pop("settings"))

        expected_version = d.pop("expectedVersion", UNSET)

        guardrail_write_request = cls(
            marketplace_account_id=marketplace_account_id,
            settings=settings,
            expected_version=expected_version,
        )

        return guardrail_write_request

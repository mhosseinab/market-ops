from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.guardrail_settings import GuardrailSettings


T = TypeVar("T", bound="GuardrailWriteRequest")


@_attrs_define
class GuardrailWriteRequest:
    """
    Attributes:
        marketplace_account_id (UUID):
        settings (GuardrailSettings): The L3 commercial guardrails (PD-3 item 6).
    """

    marketplace_account_id: UUID
    settings: GuardrailSettings

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        settings = self.settings.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "settings": settings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.guardrail_settings import GuardrailSettings

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        settings = GuardrailSettings.from_dict(d.pop("settings"))

        guardrail_write_request = cls(
            marketplace_account_id=marketplace_account_id,
            settings=settings,
        )

        return guardrail_write_request

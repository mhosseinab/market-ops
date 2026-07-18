from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ConnectorAccountRef")


@_attrs_define
class ConnectorAccountRef:
    """References the marketplace account a connector operation targets.

    Attributes:
        marketplace_account_id (UUID): Marketplace account (PRD §15.1) the operation applies to.
    """

    marketplace_account_id: UUID

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        connector_account_ref = cls(
            marketplace_account_id=marketplace_account_id,
        )

        return connector_account_ref

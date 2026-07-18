from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ConnectorConnectRequest")


@_attrs_define
class ConnectorConnectRequest:
    """Connect request. The authorization code is the value DK redirects back after the seller approves access (Seller
    Academy token guide, §0.1). It is exchanged server-side for tokens and never persisted in plaintext.

        Attributes:
            marketplace_account_id (UUID): Marketplace account (PRD §15.1) to connect.
            authorization_code (str): One-time DK authorization code exchanged for tokens.
    """

    marketplace_account_id: UUID
    authorization_code: str

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        authorization_code = self.authorization_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "authorizationCode": authorization_code,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        authorization_code = d.pop("authorizationCode")

        connector_connect_request = cls(
            marketplace_account_id=marketplace_account_id,
            authorization_code=authorization_code,
        )

        return connector_connect_request

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PairingCode")


@_attrs_define
class PairingCode:
    """A short-lived, single-use extension pairing code (EXT-001), minted by a logged-in human and displayed for entry into
    the extension. It is bound to one marketplace account and expires quickly.

        Attributes:
            code (str): The one-time pairing code to enter in the extension.
            marketplace_account_id (UUID): The marketplace account the resulting credential is scoped to.
            expires_at (datetime.datetime): Absolute expiry; a code at/after this instant fails closed.
    """

    code: str
    marketplace_account_id: UUID
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        marketplace_account_id = str(self.marketplace_account_id)

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "marketplaceAccountId": marketplace_account_id,
                "expiresAt": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        pairing_code = cls(
            code=code,
            marketplace_account_id=marketplace_account_id,
            expires_at=expires_at,
        )

        return pairing_code

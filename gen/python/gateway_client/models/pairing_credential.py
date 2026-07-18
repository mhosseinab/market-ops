from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PairingCredential")


@_attrs_define
class PairingCredential:
    """A scoped capture/overlay credential (EXT-001) issued for a claimed pairing code. It authorizes ONLY
    /observation/capture and is bound to one marketplace account. It is NEVER a seller-API token; the extension stores
    only this value.

        Attributes:
            credential (str): The raw capture credential; presented as a Bearer on uploads.
            credential_id (UUID): Stable id of the credential record (for revocation/audit).
            marketplace_account_id (UUID): The marketplace account this credential is scoped to.
            expires_at (datetime.datetime): Absolute expiry; an upload at/after this instant fails closed.
    """

    credential: str
    credential_id: UUID
    marketplace_account_id: UUID
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        credential = self.credential

        credential_id = str(self.credential_id)

        marketplace_account_id = str(self.marketplace_account_id)

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "credential": credential,
                "credentialId": credential_id,
                "marketplaceAccountId": marketplace_account_id,
                "expiresAt": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        credential = d.pop("credential")

        credential_id = UUID(d.pop("credentialId"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        pairing_credential = cls(
            credential=credential,
            credential_id=credential_id,
            marketplace_account_id=marketplace_account_id,
            expires_at=expires_at,
        )

        return pairing_credential

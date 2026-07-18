from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PendingReconciliationAction")


@_attrs_define
class PendingReconciliationAction:
    """One EXE-003 action awaiting reconciliation (an unknown external result).

    Attributes:
        action_id (UUID):
        card_id (UUID):
        idempotency_key (str):
        created_at (datetime.datetime):
    """

    action_id: UUID
    card_id: UUID
    idempotency_key: str
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        card_id = str(self.card_id)

        idempotency_key = self.idempotency_key

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "cardId": card_id,
                "idempotencyKey": idempotency_key,
                "createdAt": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        card_id = UUID(d.pop("cardId"))

        idempotency_key = d.pop("idempotencyKey")

        created_at = datetime.datetime.fromisoformat(d.pop("createdAt"))

        pending_reconciliation_action = cls(
            action_id=action_id,
            card_id=card_id,
            idempotency_key=idempotency_key,
            created_at=created_at,
        )

        return pending_reconciliation_action

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="NotificationAckRequest")


@_attrs_define
class NotificationAckRequest:
    """Acknowledge (mark read) one notification for an account.

    Attributes:
        marketplace_account_id (UUID):
        notification_id (UUID):
    """

    marketplace_account_id: UUID
    notification_id: UUID

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        notification_id = str(self.notification_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "notificationId": notification_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        notification_id = UUID(d.pop("notificationId"))

        notification_ack_request = cls(
            marketplace_account_id=marketplace_account_id,
            notification_id=notification_id,
        )

        return notification_ack_request

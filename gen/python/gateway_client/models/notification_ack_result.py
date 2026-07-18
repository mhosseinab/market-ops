from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="NotificationAckResult")


@_attrs_define
class NotificationAckResult:
    """The idempotent acknowledgement result. `changed` is false when the notification was already read or not owned by the
    account (a no-op).

        Attributes:
            notification_id (UUID):
            changed (bool):
    """

    notification_id: UUID
    changed: bool

    def to_dict(self) -> dict[str, Any]:
        notification_id = str(self.notification_id)

        changed = self.changed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "notificationId": notification_id,
                "changed": changed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        notification_id = UUID(d.pop("notificationId"))

        changed = d.pop("changed")

        notification_ack_result = cls(
            notification_id=notification_id,
            changed=changed,
        )

        return notification_ack_result

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.notification import Notification


T = TypeVar("T", bound="NotificationFeed")


@_attrs_define
class NotificationFeed:
    """The in-app notification feed for an account (NOT-001), newest first, with the current unread count for the badge.

    Attributes:
        marketplace_account_id (UUID):
        unread_count (int):
        notifications (list[Notification]):
    """

    marketplace_account_id: UUID
    unread_count: int
    notifications: list[Notification]

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        unread_count = self.unread_count

        notifications = []
        for notifications_item_data in self.notifications:
            notifications_item = notifications_item_data.to_dict()
            notifications.append(notifications_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "unreadCount": unread_count,
                "notifications": notifications,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.notification import Notification

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        unread_count = d.pop("unreadCount")

        notifications = []
        _notifications = d.pop("notifications")
        for notifications_item_data in _notifications:
            notifications_item = Notification.from_dict(notifications_item_data)

            notifications.append(notifications_item)

        notification_feed = cls(
            marketplace_account_id=marketplace_account_id,
            unread_count=unread_count,
            notifications=notifications,
        )

        return notification_feed

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.notification import Notification


T = TypeVar("T", bound="NotificationFeed")


@_attrs_define
class NotificationFeed:
    """The in-app notification feed for an account (NOT-001), newest first, with the current account-wide unread count for
    the badge and bounded keyset pagination (§17). `hasMore` reports whether older notifications exist beyond this page;
    `nextCursor` is the opaque continuation token to pass back as `cursor` to fetch them (null on the last page).
    `unreadCount` stays the ACCOUNT-WIDE badge count, not the count within the returned page.

        Attributes:
            marketplace_account_id (UUID):
            unread_count (int):
            notifications (list[Notification]):
            has_more (bool): True when older notifications exist beyond this page (an additional page can be fetched with
                `nextCursor`). False on the last page.
            next_cursor (None | str | Unset): Opaque keyset continuation token for the next (older) page, or null when
                `hasMore` is false. Pass it back verbatim as the `cursor` query param.
    """

    marketplace_account_id: UUID
    unread_count: int
    notifications: list[Notification]
    has_more: bool
    next_cursor: None | str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        unread_count = self.unread_count

        notifications = []
        for notifications_item_data in self.notifications:
            notifications_item = notifications_item_data.to_dict()
            notifications.append(notifications_item)

        has_more = self.has_more

        next_cursor: None | str | Unset
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "unreadCount": unread_count,
                "notifications": notifications,
                "hasMore": has_more,
            }
        )
        if next_cursor is not UNSET:
            field_dict["nextCursor"] = next_cursor

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

        has_more = d.pop("hasMore")

        def _parse_next_cursor(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        next_cursor = _parse_next_cursor(d.pop("nextCursor", UNSET))

        notification_feed = cls(
            marketplace_account_id=marketplace_account_id,
            unread_count=unread_count,
            notifications=notifications,
            has_more=has_more,
            next_cursor=next_cursor,
        )

        return notification_feed

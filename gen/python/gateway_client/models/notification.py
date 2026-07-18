from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.notification_category import NotificationCategory
from ..models.notification_severity import NotificationSeverity
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.notification_body_params import NotificationBodyParams


T = TypeVar("T", bound="Notification")


@_attrs_define
class Notification:
    """One in-app notification. `eventId` is the SHARED product event id — the same id the daily email digest references
    (NOT-001). `titleKey`/`bodyKey` are locale catalog KEYS with named slots in `bodyParams` (LOC-002); the surface
    renders copy, the core stores none. `readAt` is absent when unread.

        Attributes:
            id (UUID):
            event_id (UUID):
            category (NotificationCategory):
            severity (NotificationSeverity):
            bypass_digest (bool): True for execution/safety failures — delivered immediately, bypassing the batched daily
                digest, and never shed.
            title_key (str):
            body_key (str):
            body_params (NotificationBodyParams):
            created_at (datetime.datetime):
            read_at (datetime.datetime | Unset):
    """

    id: UUID
    event_id: UUID
    category: NotificationCategory
    severity: NotificationSeverity
    bypass_digest: bool
    title_key: str
    body_key: str
    body_params: NotificationBodyParams
    created_at: datetime.datetime
    read_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        event_id = str(self.event_id)

        category = self.category.value

        severity = self.severity.value

        bypass_digest = self.bypass_digest

        title_key = self.title_key

        body_key = self.body_key

        body_params = self.body_params.to_dict()

        created_at = self.created_at.isoformat()

        read_at: str | Unset = UNSET
        if not isinstance(self.read_at, Unset):
            read_at = self.read_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "eventId": event_id,
                "category": category,
                "severity": severity,
                "bypassDigest": bypass_digest,
                "titleKey": title_key,
                "bodyKey": body_key,
                "bodyParams": body_params,
                "createdAt": created_at,
            }
        )
        if read_at is not UNSET:
            field_dict["readAt"] = read_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.notification_body_params import NotificationBodyParams

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        event_id = UUID(d.pop("eventId"))

        category = NotificationCategory(d.pop("category"))

        severity = NotificationSeverity(d.pop("severity"))

        bypass_digest = d.pop("bypassDigest")

        title_key = d.pop("titleKey")

        body_key = d.pop("bodyKey")

        body_params = NotificationBodyParams.from_dict(d.pop("bodyParams"))

        created_at = datetime.datetime.fromisoformat(d.pop("createdAt"))

        _read_at = d.pop("readAt", UNSET)
        read_at: datetime.datetime | Unset
        if isinstance(_read_at, Unset):
            read_at = UNSET
        else:
            read_at = datetime.datetime.fromisoformat(_read_at)

        notification = cls(
            id=id,
            event_id=event_id,
            category=category,
            severity=severity,
            bypass_digest=bypass_digest,
            title_key=title_key,
            body_key=body_key,
            body_params=body_params,
            created_at=created_at,
            read_at=read_at,
        )

        return notification

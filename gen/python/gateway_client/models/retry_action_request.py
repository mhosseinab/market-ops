from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="RetryActionRequest")


@_attrs_define
class RetryActionRequest:
    """Request to retry an eligible failed action (EXE-003 / CHAT-074).

    Attributes:
        action_id (UUID):
    """

    action_id: UUID

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        retry_action_request = cls(
            action_id=action_id,
        )

        return retry_action_request

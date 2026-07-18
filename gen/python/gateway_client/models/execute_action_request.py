from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ExecuteActionRequest")


@_attrs_define
class ExecuteActionRequest:
    """Request to revalidate and execute an approved card (§7.5).

    Attributes:
        card_id (UUID):
    """

    card_id: UUID

    def to_dict(self) -> dict[str, Any]:
        card_id = str(self.card_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cardId": card_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        card_id = UUID(d.pop("cardId"))

        execute_action_request = cls(
            card_id=card_id,
        )

        return execute_action_request

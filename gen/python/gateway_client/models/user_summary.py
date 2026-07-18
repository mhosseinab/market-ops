from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.user_role import UserRole

T = TypeVar("T", bound="UserSummary")


@_attrs_define
class UserSummary:
    """
    Attributes:
        id (UUID):
        email (str):
        role (UserRole): Product role (PRD §2.2). Owner governs commercial boundaries and users; Operator executes day-
            to-day within Owner-defined permissions; Internal diagnoses data/execution and cannot change seller commercial
            rules.
        created_at (datetime.datetime):
    """

    id: UUID
    email: str
    role: UserRole
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        email = self.email

        role = self.role.value

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "email": email,
                "role": role,
                "createdAt": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        email = d.pop("email")

        role = UserRole(d.pop("role"))

        created_at = datetime.datetime.fromisoformat(d.pop("createdAt"))

        user_summary = cls(
            id=id,
            email=email,
            role=role,
            created_at=created_at,
        )

        return user_summary

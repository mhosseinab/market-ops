from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.user_role import UserRole

T = TypeVar("T", bound="SessionInfo")


@_attrs_define
class SessionInfo:
    """Identity of the authenticated session. This is the single shape both chat and screens read the current principal
    from; role drives the shared permission matrix (ACC-002).

        Attributes:
            user_id (UUID): The authenticated user (PRD §15.1).
            organization_id (UUID): The organization the user belongs to.
            email (str): The user's email.
            role (UserRole): Product role (PRD §2.2). Owner governs commercial boundaries and users; Operator executes day-
                to-day within Owner-defined permissions; Internal diagnoses data/execution and cannot change seller commercial
                rules.
            expires_at (datetime.datetime): When the session expires (RFC 3339, UTC).
    """

    user_id: UUID
    organization_id: UUID
    email: str
    role: UserRole
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        user_id = str(self.user_id)

        organization_id = str(self.organization_id)

        email = self.email

        role = self.role.value

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "userId": user_id,
                "organizationId": organization_id,
                "email": email,
                "role": role,
                "expiresAt": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        user_id = UUID(d.pop("userId"))

        organization_id = UUID(d.pop("organizationId"))

        email = d.pop("email")

        role = UserRole(d.pop("role"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        session_info = cls(
            user_id=user_id,
            organization_id=organization_id,
            email=email,
            role=role,
            expires_at=expires_at,
        )

        return session_info

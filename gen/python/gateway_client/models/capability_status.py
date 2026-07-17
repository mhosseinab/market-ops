from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.connector_capability import ConnectorCapability
from ..models.connector_capability_state import ConnectorCapabilityState
from ..types import UNSET, Unset

T = TypeVar("T", bound="CapabilityStatus")


@_attrs_define
class CapabilityStatus:
    """One capability's current status and last-verified time (ACC-001).

    Attributes:
        capability (ConnectorCapability): The nine connector capabilities enumerated in PRD §15.2. Each is reported
            independently; the marketplace name never gates behavior.
        status (ConnectorCapabilityState): Capability status (PRD §15.2). Starts Unknown; becomes Supported only after a
            probe confirms behavior. Unknown never enables dependent UI.
        last_verified (datetime.datetime | Unset): When a probe last set this status (RFC 3339, UTC). Absent until the
            first probe runs; a historical value never reads as current.
        detail (str | Unset): Recovery-oriented reason for a non-Supported status (ACC-003). Free text only; carries no
            authority (PRD §8).
    """

    capability: ConnectorCapability
    status: ConnectorCapabilityState
    last_verified: datetime.datetime | Unset = UNSET
    detail: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        capability = self.capability.value

        status = self.status.value

        last_verified: str | Unset = UNSET
        if not isinstance(self.last_verified, Unset):
            last_verified = self.last_verified.isoformat()

        detail = self.detail

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capability": capability,
                "status": status,
            }
        )
        if last_verified is not UNSET:
            field_dict["lastVerified"] = last_verified
        if detail is not UNSET:
            field_dict["detail"] = detail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        capability = ConnectorCapability(d.pop("capability"))

        status = ConnectorCapabilityState(d.pop("status"))

        _last_verified = d.pop("lastVerified", UNSET)
        last_verified: datetime.datetime | Unset
        if isinstance(_last_verified, Unset):
            last_verified = UNSET
        else:
            last_verified = datetime.datetime.fromisoformat(_last_verified)

        detail = d.pop("detail", UNSET)

        capability_status = cls(
            capability=capability,
            status=status,
            last_verified=last_verified,
            detail=detail,
        )

        return capability_status

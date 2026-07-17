from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.evidence_version import EvidenceVersion


T = TypeVar("T", bound="ApprovalBinding")


@_attrs_define
class ApprovalBinding:
    """The APR-001 version binding of an approval control: the exact action id, parameter/context/policy/cost versions,
    evidence versions, and expiry. ANY change to a bound dimension, or a reached expiry, invalidates the control.

        Attributes:
            action_id (UUID):
            parameter_version (int):
            context_version (int):
            policy_version (int):
            cost_profile_version (int):
            evidence_versions (list[EvidenceVersion]):
            expires_at (datetime.datetime):
    """

    action_id: UUID
    parameter_version: int
    context_version: int
    policy_version: int
    cost_profile_version: int
    evidence_versions: list[EvidenceVersion]
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        parameter_version = self.parameter_version

        context_version = self.context_version

        policy_version = self.policy_version

        cost_profile_version = self.cost_profile_version

        evidence_versions = []
        for evidence_versions_item_data in self.evidence_versions:
            evidence_versions_item = evidence_versions_item_data.to_dict()
            evidence_versions.append(evidence_versions_item)

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "parameterVersion": parameter_version,
                "contextVersion": context_version,
                "policyVersion": policy_version,
                "costProfileVersion": cost_profile_version,
                "evidenceVersions": evidence_versions,
                "expiresAt": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.evidence_version import EvidenceVersion

        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        parameter_version = d.pop("parameterVersion")

        context_version = d.pop("contextVersion")

        policy_version = d.pop("policyVersion")

        cost_profile_version = d.pop("costProfileVersion")

        evidence_versions = []
        _evidence_versions = d.pop("evidenceVersions")
        for evidence_versions_item_data in _evidence_versions:
            evidence_versions_item = EvidenceVersion.from_dict(evidence_versions_item_data)

            evidence_versions.append(evidence_versions_item)

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        approval_binding = cls(
            action_id=action_id,
            parameter_version=parameter_version,
            context_version=context_version,
            policy_version=policy_version,
            cost_profile_version=cost_profile_version,
            evidence_versions=evidence_versions,
            expires_at=expires_at,
        )

        return approval_binding

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EvidenceVersion")


@_attrs_define
class EvidenceVersion:
    """One cited observation bound to the exact evidence version used (APR-001).

    Attributes:
        observation_id (UUID):
        version (int):
    """

    observation_id: UUID
    version: int

    def to_dict(self) -> dict[str, Any]:
        observation_id = str(self.observation_id)

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observationId": observation_id,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observation_id = UUID(d.pop("observationId"))

        version = d.pop("version")

        evidence_version = cls(
            observation_id=observation_id,
            version=version,
        )

        return evidence_version

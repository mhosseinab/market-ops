from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.quality_state import QualityState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ChatEvidenceRef")


@_attrs_define
class ChatEvidenceRef:
    """One cited evidence reference inside a chat envelope statement.

    Attributes:
        observation_id (UUID):
        quality (QualityState | Unset): The SIX evidence-quality states (PRD §10.3, OBS-003). The set is closed; each
            state has a fixed display/recommend/execute consequence. An expired value is `stale` and can never satisfy a
            current-data gate (OBS-004).
    """

    observation_id: UUID
    quality: QualityState | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        observation_id = str(self.observation_id)

        quality: str | Unset = UNSET
        if not isinstance(self.quality, Unset):
            quality = self.quality.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observationId": observation_id,
            }
        )
        if quality is not UNSET:
            field_dict["quality"] = quality

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observation_id = UUID(d.pop("observationId"))

        _quality = d.pop("quality", UNSET)
        quality: QualityState | Unset
        if isinstance(_quality, Unset):
            quality = UNSET
        else:
            quality = QualityState(_quality)

        chat_evidence_ref = cls(
            observation_id=observation_id,
            quality=quality,
        )

        return chat_evidence_ref

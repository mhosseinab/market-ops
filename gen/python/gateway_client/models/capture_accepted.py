from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.quality_state import QualityState
from ..types import UNSET, Unset

T = TypeVar("T", bound="CaptureAccepted")


@_attrs_define
class CaptureAccepted:
    """Result of a capture upload. `deduped` marks an equivalent replay that created no duplicate current offer while
    retaining provenance (OBS-008).

        Attributes:
            deduped (bool):
            quality (QualityState): The SIX evidence-quality states (PRD §10.3, OBS-003). The set is closed; each state has
                a fixed display/recommend/execute consequence. An expired value is `stale` and can never satisfy a current-data
                gate (OBS-004).
            observation_id (None | Unset | UUID): The append-only evidence row id; null when deduped.
    """

    deduped: bool
    quality: QualityState
    observation_id: None | Unset | UUID = UNSET

    def to_dict(self) -> dict[str, Any]:
        deduped = self.deduped

        quality = self.quality.value

        observation_id: None | str | Unset
        if isinstance(self.observation_id, Unset):
            observation_id = UNSET
        elif isinstance(self.observation_id, UUID):
            observation_id = str(self.observation_id)
        else:
            observation_id = self.observation_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deduped": deduped,
                "quality": quality,
            }
        )
        if observation_id is not UNSET:
            field_dict["observationId"] = observation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deduped = d.pop("deduped")

        quality = QualityState(d.pop("quality"))

        def _parse_observation_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observation_id_type_0 = UUID(data)

                return observation_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        observation_id = _parse_observation_id(d.pop("observationId", UNSET))

        capture_accepted = cls(
            deduped=deduped,
            quality=quality,
            observation_id=observation_id,
        )

        return capture_accepted

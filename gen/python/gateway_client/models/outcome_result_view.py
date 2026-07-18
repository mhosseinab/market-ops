from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.outcome_result_view_confidence import OutcomeResultViewConfidence
from ..models.outcome_result_view_result import OutcomeResultViewResult
from ..types import UNSET, Unset

T = TypeVar("T", bound="OutcomeResultView")


@_attrs_define
class OutcomeResultView:
    """The §15.3 result + confidence of a closed outcome window.

    Attributes:
        result (OutcomeResultViewResult):
        confidence (OutcomeResultViewConfidence):
        computed_at (datetime.datetime | Unset):
    """

    result: OutcomeResultViewResult
    confidence: OutcomeResultViewConfidence
    computed_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result = self.result.value

        confidence = self.confidence.value

        computed_at: str | Unset = UNSET
        if not isinstance(self.computed_at, Unset):
            computed_at = self.computed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "result": result,
                "confidence": confidence,
            }
        )
        if computed_at is not UNSET:
            field_dict["computedAt"] = computed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        result = OutcomeResultViewResult(d.pop("result"))

        confidence = OutcomeResultViewConfidence(d.pop("confidence"))

        _computed_at = d.pop("computedAt", UNSET)
        computed_at: datetime.datetime | Unset
        if isinstance(_computed_at, Unset):
            computed_at = UNSET
        else:
            computed_at = datetime.datetime.fromisoformat(_computed_at)

        outcome_result_view = cls(
            result=result,
            confidence=confidence,
            computed_at=computed_at,
        )

        return outcome_result_view

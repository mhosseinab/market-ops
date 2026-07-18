from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.outcome_summary_confidence import OutcomeSummaryConfidence
from ..models.outcome_summary_result import OutcomeSummaryResult
from ..types import UNSET, Unset

T = TypeVar("T", bound="OutcomeSummary")


@_attrs_define
class OutcomeSummary:
    """One row of the outcomes queue (OUT-001, PD-3 item 5).

    Attributes:
        action_id (UUID):
        opened_at (datetime.datetime):
        closes_at (datetime.datetime):
        card_id (UUID | Unset):
        result (OutcomeSummaryResult | Unset): The §15.3 result, present only once the window has closed.
        confidence (OutcomeSummaryConfidence | Unset):
    """

    action_id: UUID
    opened_at: datetime.datetime
    closes_at: datetime.datetime
    card_id: UUID | Unset = UNSET
    result: OutcomeSummaryResult | Unset = UNSET
    confidence: OutcomeSummaryConfidence | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        opened_at = self.opened_at.isoformat()

        closes_at = self.closes_at.isoformat()

        card_id: str | Unset = UNSET
        if not isinstance(self.card_id, Unset):
            card_id = str(self.card_id)

        result: str | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.value

        confidence: str | Unset = UNSET
        if not isinstance(self.confidence, Unset):
            confidence = self.confidence.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "openedAt": opened_at,
                "closesAt": closes_at,
            }
        )
        if card_id is not UNSET:
            field_dict["cardId"] = card_id
        if result is not UNSET:
            field_dict["result"] = result
        if confidence is not UNSET:
            field_dict["confidence"] = confidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        opened_at = datetime.datetime.fromisoformat(d.pop("openedAt"))

        closes_at = datetime.datetime.fromisoformat(d.pop("closesAt"))

        _card_id = d.pop("cardId", UNSET)
        card_id: UUID | Unset
        if isinstance(_card_id, Unset):
            card_id = UNSET
        else:
            card_id = UUID(_card_id)

        _result = d.pop("result", UNSET)
        result: OutcomeSummaryResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = OutcomeSummaryResult(_result)

        _confidence = d.pop("confidence", UNSET)
        confidence: OutcomeSummaryConfidence | Unset
        if isinstance(_confidence, Unset):
            confidence = UNSET
        else:
            confidence = OutcomeSummaryConfidence(_confidence)

        outcome_summary = cls(
            action_id=action_id,
            opened_at=opened_at,
            closes_at=closes_at,
            card_id=card_id,
            result=result,
            confidence=confidence,
        )

        return outcome_summary

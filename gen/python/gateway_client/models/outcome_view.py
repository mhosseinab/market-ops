from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outcome_result_view import OutcomeResultView


T = TypeVar("T", bound="OutcomeView")


@_attrs_define
class OutcomeView:
    """An action's OUT-001 seven-day outcome window and, once closed, its §15.3 result. `result` is absent while the window
    is still open.

        Attributes:
            action_id (UUID):
            opened_at (datetime.datetime):
            closes_at (datetime.datetime):
            result (OutcomeResultView | Unset): The §15.3 result + confidence of a closed outcome window.
    """

    action_id: UUID
    opened_at: datetime.datetime
    closes_at: datetime.datetime
    result: OutcomeResultView | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        opened_at = self.opened_at.isoformat()

        closes_at = self.closes_at.isoformat()

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "actionId": action_id,
                "openedAt": opened_at,
                "closesAt": closes_at,
            }
        )
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outcome_result_view import OutcomeResultView

        d = dict(src_dict)
        action_id = UUID(d.pop("actionId"))

        opened_at = datetime.datetime.fromisoformat(d.pop("openedAt"))

        closes_at = datetime.datetime.fromisoformat(d.pop("closesAt"))

        _result = d.pop("result", UNSET)
        result: OutcomeResultView | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = OutcomeResultView.from_dict(_result)

        outcome_view = cls(
            action_id=action_id,
            opened_at=opened_at,
            closes_at=closes_at,
            result=result,
        )

        return outcome_view

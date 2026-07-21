from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.latest_briefing_read_provenance import LatestBriefingReadProvenance
from ..models.latest_briefing_read_state import LatestBriefingReadState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.daily_briefing import DailyBriefing


T = TypeVar("T", bound="LatestBriefingRead")


@_attrs_define
class LatestBriefingRead:
    """Provenance-aware result for a bounded latest-briefing lookup. When `state` is `available`, `provenance` is
    `stored_briefing` and `briefing` is required. When `state` is `never_generated`, `provenance` is `none` and
    `briefing` is omitted. Transport/storage errors are ErrorEnvelope responses, never a fabricated date or a
    `never_generated` success.

        Attributes:
            state (LatestBriefingReadState):
            provenance (LatestBriefingReadProvenance):
            briefing (DailyBriefing | Unset): The stored once-per-business-day briefing (CHAT-010). Its events carry the
                SAME ids and ORDER as the Today feed for the account/day (generated from the one ranking, never a re-
                computation).
    """

    state: LatestBriefingReadState
    provenance: LatestBriefingReadProvenance
    briefing: DailyBriefing | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state = self.state.value

        provenance = self.provenance.value

        briefing: dict[str, Any] | Unset = UNSET
        if not isinstance(self.briefing, Unset):
            briefing = self.briefing.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "provenance": provenance,
            }
        )
        if briefing is not UNSET:
            field_dict["briefing"] = briefing

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.daily_briefing import DailyBriefing

        d = dict(src_dict)
        state = LatestBriefingReadState(d.pop("state"))

        provenance = LatestBriefingReadProvenance(d.pop("provenance"))

        _briefing = d.pop("briefing", UNSET)
        briefing: DailyBriefing | Unset
        if isinstance(_briefing, Unset):
            briefing = UNSET
        else:
            briefing = DailyBriefing.from_dict(_briefing)

        latest_briefing_read = cls(
            state=state,
            provenance=provenance,
            briefing=briefing,
        )

        return latest_briefing_read

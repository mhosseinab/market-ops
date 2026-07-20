from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.conflict_evidence_state import ConflictEvidenceState

if TYPE_CHECKING:
    from ..models.conflict_route_evidence import ConflictRouteEvidence


T = TypeVar("T", bound="ConflictEvidence")


@_attrs_define
class ConflictEvidence:
    """Cross-route disagreeing evidence behind a `conflicted` Observed Offer (issue #94, §16 / §10.3). Surfaces the LATEST
    still-in-window observation PER ROUTE (raw value/unit, availability, capture time, freshness deadline) so the
    operator can inspect WHY the offer is blocked. Data is exposed VERBATIM from the append-only in-window evidence —
    never recomputed and never inferred. The action stays fail-closed blocked regardless of this view.

        Attributes:
            state (ConflictEvidenceState): `available`: at least two disagreeing in-window routes were found and are listed
                in `routes`. `unavailable`: the comparison evidence is missing/incomplete (fewer than two routes are still in
                window) — an EXPLICIT read-model error, NOT a complete panel. The client renders the error state and NEVER
                infers the missing route evidence.
            routes (list[ConflictRouteEvidence]): The disagreeing routes' latest in-window evidence; empty when `state` is
                `unavailable`.
    """

    state: ConflictEvidenceState
    routes: list[ConflictRouteEvidence]

    def to_dict(self) -> dict[str, Any]:
        state = self.state.value

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "routes": routes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.conflict_route_evidence import ConflictRouteEvidence

        d = dict(src_dict)
        state = ConflictEvidenceState(d.pop("state"))

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = ConflictRouteEvidence.from_dict(routes_item_data)

            routes.append(routes_item)

        conflict_evidence = cls(
            state=state,
            routes=routes,
        )

        return conflict_evidence

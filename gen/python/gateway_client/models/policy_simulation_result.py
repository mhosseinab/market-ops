from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.contribution import Contribution
    from ..models.policy_blocker import PolicyBlocker
    from ..models.policy_proposal import PolicyProposal


T = TypeVar("T", bound="PolicySimulationResult")


@_attrs_define
class PolicySimulationResult:
    """The simulated contribution and policy result. `simulation` is always true and `approvable` is always false: a
    simulation NEVER carries an approval control (§8, §12.3, never-cut). `proposal` is present only when the policy
    stages accepted a price; otherwise `blockers` lists the typed reasons in policy order.

        Attributes:
            simulation (bool): Always true — this result is non-executable.
            approvable (bool): Always false — a simulation carries no approval control.
            contribution (Contribution): The deterministic §9.2 contribution: the exact amount, its breakdown, the readiness
                that governs executability (only Complete is executable), and the versioned rounding-rule identifier that
                produced it (reproducible per CST-002).
            blockers (list[PolicyBlocker]):
            proposal (PolicyProposal | Unset): An accepted policy result: a proposed price and its contribution. Present
                only when every hard stage passed and the contribution is strictly positive. It is NOT an approval control.
    """

    simulation: bool
    approvable: bool
    contribution: Contribution
    blockers: list[PolicyBlocker]
    proposal: PolicyProposal | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        simulation = self.simulation

        approvable = self.approvable

        contribution = self.contribution.to_dict()

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        proposal: dict[str, Any] | Unset = UNSET
        if not isinstance(self.proposal, Unset):
            proposal = self.proposal.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "simulation": simulation,
                "approvable": approvable,
                "contribution": contribution,
                "blockers": blockers,
            }
        )
        if proposal is not UNSET:
            field_dict["proposal"] = proposal

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.contribution import Contribution
        from ..models.policy_blocker import PolicyBlocker
        from ..models.policy_proposal import PolicyProposal

        d = dict(src_dict)
        simulation = d.pop("simulation")

        approvable = d.pop("approvable")

        contribution = Contribution.from_dict(d.pop("contribution"))

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = PolicyBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        _proposal = d.pop("proposal", UNSET)
        proposal: PolicyProposal | Unset
        if isinstance(_proposal, Unset):
            proposal = UNSET
        else:
            proposal = PolicyProposal.from_dict(_proposal)

        policy_simulation_result = cls(
            simulation=simulation,
            approvable=approvable,
            contribution=contribution,
            blockers=blockers,
            proposal=proposal,
        )

        return policy_simulation_result

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.policy_objective import PolicyObjective
from ..models.policy_strategy import PolicyStrategy
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount
    from ..models.policy_boundary import PolicyBoundary


T = TypeVar("T", bound="PolicyConfig")


@_attrs_define
class PolicyConfig:
    """The policy configuration for a simulation. `movementCapBasisPoints` and `cooldownSeconds` are optional; omitting
    them uses the §9.3 defaults (5%, 60m). A looser value (a larger cap or shorter cooldown) is rejected (PRC-004).

        Attributes:
            boundary (PolicyBoundary): The marketplace price boundary (stage 1, §9.2). `known` false is an UNKNOWN boundary
                and blocks (§16). Min/Max are required when known.
            contribution_floor (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD
                §9.1). Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost
                amount is representable because the account's entry currency is known; it stays excluded from executable paths
                until S16+S35.
            strategy (PolicyStrategy): The selected pricing strategy (stage 5, §9.3). Closed set for P0.
            strategy_enabled (bool):
            objective (PolicyObjective): The optimization objective (stage 6, §9.3).
            movement_cap_basis_points (int | Unset): Maximum price movement in basis points (≤ 500; default 500).
            cooldown_seconds (int | Unset): Minimum interval between actions in seconds (≥ 3600; default 3600).
            reference (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD
                §9.1). Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost
                amount is representable because the account's entry currency is known; it stays excluded from executable paths
                until S16+S35.
            undercut_basis_points (int | Unset): Undercut depth in basis points for the `undercut` strategy.
    """

    boundary: PolicyBoundary
    contribution_floor: MoneyAmount
    strategy: PolicyStrategy
    strategy_enabled: bool
    objective: PolicyObjective
    movement_cap_basis_points: int | Unset = UNSET
    cooldown_seconds: int | Unset = UNSET
    reference: MoneyAmount | Unset = UNSET
    undercut_basis_points: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        boundary = self.boundary.to_dict()

        contribution_floor = self.contribution_floor.to_dict()

        strategy = self.strategy.value

        strategy_enabled = self.strategy_enabled

        objective = self.objective.value

        movement_cap_basis_points = self.movement_cap_basis_points

        cooldown_seconds = self.cooldown_seconds

        reference: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reference, Unset):
            reference = self.reference.to_dict()

        undercut_basis_points = self.undercut_basis_points

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "boundary": boundary,
                "contributionFloor": contribution_floor,
                "strategy": strategy,
                "strategyEnabled": strategy_enabled,
                "objective": objective,
            }
        )
        if movement_cap_basis_points is not UNSET:
            field_dict["movementCapBasisPoints"] = movement_cap_basis_points
        if cooldown_seconds is not UNSET:
            field_dict["cooldownSeconds"] = cooldown_seconds
        if reference is not UNSET:
            field_dict["reference"] = reference
        if undercut_basis_points is not UNSET:
            field_dict["undercutBasisPoints"] = undercut_basis_points

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount
        from ..models.policy_boundary import PolicyBoundary

        d = dict(src_dict)
        boundary = PolicyBoundary.from_dict(d.pop("boundary"))

        contribution_floor = MoneyAmount.from_dict(d.pop("contributionFloor"))

        strategy = PolicyStrategy(d.pop("strategy"))

        strategy_enabled = d.pop("strategyEnabled")

        objective = PolicyObjective(d.pop("objective"))

        movement_cap_basis_points = d.pop("movementCapBasisPoints", UNSET)

        cooldown_seconds = d.pop("cooldownSeconds", UNSET)

        _reference = d.pop("reference", UNSET)
        reference: MoneyAmount | Unset
        if isinstance(_reference, Unset):
            reference = UNSET
        else:
            reference = MoneyAmount.from_dict(_reference)

        undercut_basis_points = d.pop("undercutBasisPoints", UNSET)

        policy_config = cls(
            boundary=boundary,
            contribution_floor=contribution_floor,
            strategy=strategy,
            strategy_enabled=strategy_enabled,
            objective=objective,
            movement_cap_basis_points=movement_cap_basis_points,
            cooldown_seconds=cooldown_seconds,
            reference=reference,
            undercut_basis_points=undercut_basis_points,
        )

        return policy_config

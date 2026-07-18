from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.policy_strategy import PolicyStrategy

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="GuardrailSettings")


@_attrs_define
class GuardrailSettings:
    """The L3 commercial guardrails (PD-3 item 6).

    Attributes:
        contribution_floor (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD
            §9.1). Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost
            amount is representable because the account's entry currency is known; it stays excluded from executable paths
            until S16+S35.
        movement_cap_basis_points (int): Maximum price movement in basis points (§9.3 default 500).
        cooldown_seconds (int): Minimum interval between actions in seconds (§9.3 default 3600).
        strategy (PolicyStrategy): The selected pricing strategy (stage 5, §9.3). Closed set for P0.
        strategy_enabled (bool):
    """

    contribution_floor: MoneyAmount
    movement_cap_basis_points: int
    cooldown_seconds: int
    strategy: PolicyStrategy
    strategy_enabled: bool

    def to_dict(self) -> dict[str, Any]:
        contribution_floor = self.contribution_floor.to_dict()

        movement_cap_basis_points = self.movement_cap_basis_points

        cooldown_seconds = self.cooldown_seconds

        strategy = self.strategy.value

        strategy_enabled = self.strategy_enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "contributionFloor": contribution_floor,
                "movementCapBasisPoints": movement_cap_basis_points,
                "cooldownSeconds": cooldown_seconds,
                "strategy": strategy,
                "strategyEnabled": strategy_enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        contribution_floor = MoneyAmount.from_dict(d.pop("contributionFloor"))

        movement_cap_basis_points = d.pop("movementCapBasisPoints")

        cooldown_seconds = d.pop("cooldownSeconds")

        strategy = PolicyStrategy(d.pop("strategy"))

        strategy_enabled = d.pop("strategyEnabled")

        guardrail_settings = cls(
            contribution_floor=contribution_floor,
            movement_cap_basis_points=movement_cap_basis_points,
            cooldown_seconds=cooldown_seconds,
            strategy=strategy,
            strategy_enabled=strategy_enabled,
        )

        return guardrail_settings

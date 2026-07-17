from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.contribution_component_kind import ContributionComponentKind
from ..models.cost_component import CostComponent

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="ContributionDeduction")


@_attrs_define
class ContributionDeduction:
    """One resolved subtraction in the contribution breakdown (PRC-001 inputs).

    Attributes:
        component (CostComponent): A cost component of the §9.2 contribution model. The set is closed. COGS and
            commission are always required; fulfillment/shipping/promotion are required when applicable to the listing;
            packaging/ads/returns are optional in P0 (an account policy may still require them).
        amount (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
            mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
            representable because the account's entry currency is known; it stays excluded from executable paths until
            S16+S35.
        kind (ContributionComponentKind): Whether a contribution component is an absolute money amount or a fixed-point
            basis-point rate applied to the rate base (§9.2). Both stay in exact money arithmetic — there is no float
            (§9.1).
        version (int):
    """

    component: CostComponent
    amount: MoneyAmount
    kind: ContributionComponentKind
    version: int

    def to_dict(self) -> dict[str, Any]:
        component = self.component.value

        amount = self.amount.to_dict()

        kind = self.kind.value

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "component": component,
                "amount": amount,
                "kind": kind,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        component = CostComponent(d.pop("component"))

        amount = MoneyAmount.from_dict(d.pop("amount"))

        kind = ContributionComponentKind(d.pop("kind"))

        version = d.pop("version")

        contribution_deduction = cls(
            component=component,
            amount=amount,
            kind=kind,
            version=version,
        )

        return contribution_deduction

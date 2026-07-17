from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.contribution_component_kind import ContributionComponentKind
from ..models.cost_component import CostComponent
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="ContributionComponentInput")


@_attrs_define
class ContributionComponentInput:
    """One §9.2 deduction with its cost-profile provenance. Exactly one of `amount` (kind `absolute`) or `rateBasisPoints`
    (kind `rate`) is meaningful. `version` is the cost-profile component version (CST-002) that produced the value,
    carried so a historical contribution reproduces the exact inputs.

        Attributes:
            component (CostComponent): A cost component of the §9.2 contribution model. The set is closed. COGS and
                commission are always required; fulfillment/shipping/promotion are required when applicable to the listing;
                packaging/ads/returns are optional in P0 (an account policy may still require them).
            kind (ContributionComponentKind): Whether a contribution component is an absolute money amount or a fixed-point
                basis-point rate applied to the rate base (§9.2). Both stay in exact money arithmetic — there is no float
                (§9.1).
            amount (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            rate_basis_points (int | Unset): Fixed-point rate in ten-thousandths (1200 = 12%), for kind `rate`.
            version (int | Unset): Cost-profile component version id (CST-002); 0 for synthetic input.
    """

    component: CostComponent
    kind: ContributionComponentKind
    amount: MoneyAmount | Unset = UNSET
    rate_basis_points: int | Unset = UNSET
    version: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        component = self.component.value

        kind = self.kind.value

        amount: dict[str, Any] | Unset = UNSET
        if not isinstance(self.amount, Unset):
            amount = self.amount.to_dict()

        rate_basis_points = self.rate_basis_points

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "component": component,
                "kind": kind,
            }
        )
        if amount is not UNSET:
            field_dict["amount"] = amount
        if rate_basis_points is not UNSET:
            field_dict["rateBasisPoints"] = rate_basis_points
        if version is not UNSET:
            field_dict["version"] = version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        component = CostComponent(d.pop("component"))

        kind = ContributionComponentKind(d.pop("kind"))

        _amount = d.pop("amount", UNSET)
        amount: MoneyAmount | Unset
        if isinstance(_amount, Unset):
            amount = UNSET
        else:
            amount = MoneyAmount.from_dict(_amount)

        rate_basis_points = d.pop("rateBasisPoints", UNSET)

        version = d.pop("version", UNSET)

        contribution_component_input = cls(
            component=component,
            kind=kind,
            amount=amount,
            rate_basis_points=rate_basis_points,
            version=version,
        )

        return contribution_component_input

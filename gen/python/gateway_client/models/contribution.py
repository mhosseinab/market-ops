from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.margin_readiness_state import MarginReadinessState

if TYPE_CHECKING:
    from ..models.contribution_deduction import ContributionDeduction
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="Contribution")


@_attrs_define
class Contribution:
    """The deterministic §9.2 contribution: the exact amount, its breakdown, the readiness that governs executability (only
    Complete is executable), and the versioned rounding-rule identifier that produced it (reproducible per CST-002).

        Attributes:
            amount (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
                mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            net_proceeds (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            deductions (list[ContributionDeduction]):
            readiness (MarginReadinessState): The four closed margin-readiness states (CST-003). Only `complete` may drive
                an executable recommendation; `partial` may show analysis but no approval control; `stale` and `missing` block.
            executable (bool): True only when readiness is Complete (§9.2 / CST-003).
            rounding_rule (str): Versioned rounding-rule identifier applied to any rate.
    """

    amount: MoneyAmount
    net_proceeds: MoneyAmount
    deductions: list[ContributionDeduction]
    readiness: MarginReadinessState
    executable: bool
    rounding_rule: str

    def to_dict(self) -> dict[str, Any]:
        amount = self.amount.to_dict()

        net_proceeds = self.net_proceeds.to_dict()

        deductions = []
        for deductions_item_data in self.deductions:
            deductions_item = deductions_item_data.to_dict()
            deductions.append(deductions_item)

        readiness = self.readiness.value

        executable = self.executable

        rounding_rule = self.rounding_rule

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "amount": amount,
                "netProceeds": net_proceeds,
                "deductions": deductions,
                "readiness": readiness,
                "executable": executable,
                "roundingRule": rounding_rule,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.contribution_deduction import ContributionDeduction
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        amount = MoneyAmount.from_dict(d.pop("amount"))

        net_proceeds = MoneyAmount.from_dict(d.pop("netProceeds"))

        deductions = []
        _deductions = d.pop("deductions")
        for deductions_item_data in _deductions:
            deductions_item = ContributionDeduction.from_dict(deductions_item_data)

            deductions.append(deductions_item)

        readiness = MarginReadinessState(d.pop("readiness"))

        executable = d.pop("executable")

        rounding_rule = d.pop("roundingRule")

        contribution = cls(
            amount=amount,
            net_proceeds=net_proceeds,
            deductions=deductions,
            readiness=readiness,
            executable=executable,
            rounding_rule=rounding_rule,
        )

        return contribution

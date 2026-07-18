from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.margin_readiness_state import MarginReadinessState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.contribution_component_input import ContributionComponentInput
    from ..models.money_amount import MoneyAmount
    from ..models.policy_config import PolicyConfig


T = TypeVar("T", bound="PolicySimulationRequest")


@_attrs_define
class PolicySimulationRequest:
    """A fully-specified what-if for the contribution + policy engines. All money must share one currency and exponent
    (§9.1). Contribution is evaluated as a function of price: at any candidate price the net seller proceeds and the
    commission rate base are that price (the P0 owned-offer model), so the policy stages see a contribution that varies
    with price. `nowRfc3339` and `lastActionAt` drive the cooldown stage; omit `lastActionAt` when there is no prior
    action.

        Attributes:
            current_price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            components (list[ContributionComponentInput]):
            readiness (MarginReadinessState): The four closed margin-readiness states (CST-003). Only `complete` may drive
                an executable recommendation; `partial` may show analysis but no approval control; `stale` and `missing` block.
            config (PolicyConfig): The policy configuration for a simulation. `movementCapBasisPoints` and `cooldownSeconds`
                are optional; omitting them uses the §9.3 defaults (5%, 60m). A looser value (a larger cap or shorter cooldown)
                is rejected (PRC-004).
            variant_id (UUID | Unset): Optional SKU the what-if is about (provenance only).
            now_rfc_3339 (datetime.datetime | Unset): Evaluation instant (defaults to server now).
            last_action_at (datetime.datetime | Unset): Instant of the last price action, for the cooldown stage.
    """

    current_price: MoneyAmount
    components: list[ContributionComponentInput]
    readiness: MarginReadinessState
    config: PolicyConfig
    variant_id: UUID | Unset = UNSET
    now_rfc_3339: datetime.datetime | Unset = UNSET
    last_action_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        current_price = self.current_price.to_dict()

        components = []
        for components_item_data in self.components:
            components_item = components_item_data.to_dict()
            components.append(components_item)

        readiness = self.readiness.value

        config = self.config.to_dict()

        variant_id: str | Unset = UNSET
        if not isinstance(self.variant_id, Unset):
            variant_id = str(self.variant_id)

        now_rfc_3339: str | Unset = UNSET
        if not isinstance(self.now_rfc_3339, Unset):
            now_rfc_3339 = self.now_rfc_3339.isoformat()

        last_action_at: str | Unset = UNSET
        if not isinstance(self.last_action_at, Unset):
            last_action_at = self.last_action_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "currentPrice": current_price,
                "components": components,
                "readiness": readiness,
                "config": config,
            }
        )
        if variant_id is not UNSET:
            field_dict["variantId"] = variant_id
        if now_rfc_3339 is not UNSET:
            field_dict["nowRfc3339"] = now_rfc_3339
        if last_action_at is not UNSET:
            field_dict["lastActionAt"] = last_action_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.contribution_component_input import ContributionComponentInput
        from ..models.money_amount import MoneyAmount
        from ..models.policy_config import PolicyConfig

        d = dict(src_dict)
        current_price = MoneyAmount.from_dict(d.pop("currentPrice"))

        components = []
        _components = d.pop("components")
        for components_item_data in _components:
            components_item = ContributionComponentInput.from_dict(components_item_data)

            components.append(components_item)

        readiness = MarginReadinessState(d.pop("readiness"))

        config = PolicyConfig.from_dict(d.pop("config"))

        _variant_id = d.pop("variantId", UNSET)
        variant_id: UUID | Unset
        if isinstance(_variant_id, Unset):
            variant_id = UNSET
        else:
            variant_id = UUID(_variant_id)

        _now_rfc_3339 = d.pop("nowRfc3339", UNSET)
        now_rfc_3339: datetime.datetime | Unset
        if isinstance(_now_rfc_3339, Unset):
            now_rfc_3339 = UNSET
        else:
            now_rfc_3339 = datetime.datetime.fromisoformat(_now_rfc_3339)

        _last_action_at = d.pop("lastActionAt", UNSET)
        last_action_at: datetime.datetime | Unset
        if isinstance(_last_action_at, Unset):
            last_action_at = UNSET
        else:
            last_action_at = datetime.datetime.fromisoformat(_last_action_at)

        policy_simulation_request = cls(
            current_price=current_price,
            components=components,
            readiness=readiness,
            config=config,
            variant_id=variant_id,
            now_rfc_3339=now_rfc_3339,
            last_action_at=last_action_at,
        )

        return policy_simulation_request

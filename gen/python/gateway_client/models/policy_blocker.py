from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.policy_blocker_code import PolicyBlockerCode
from ..models.policy_stage import PolicyStage

T = TypeVar("T", bound="PolicyBlocker")


@_attrs_define
class PolicyBlocker:
    """One typed reason a policy stage prevented a proposal (in policy order).

    Attributes:
        stage (PolicyStage): One of the six ordered policy stages (§9.3). The order is fixed; `stageOrder` on a blocker
            carries the numeric precedence.
        stage_order (int): Numeric precedence (0 = boundary … 5 = objective).
        code (PolicyBlockerCode): Stable, machine-readable reason a policy stage blocked. Free text lives only in a
            blocker's message and carries no authority (§8).
        message (str): Human-readable, non-authoritative detail (localized at the edge).
    """

    stage: PolicyStage
    stage_order: int
    code: PolicyBlockerCode
    message: str

    def to_dict(self) -> dict[str, Any]:
        stage = self.stage.value

        stage_order = self.stage_order

        code = self.code.value

        message = self.message

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "stage": stage,
                "stageOrder": stage_order,
                "code": code,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        stage = PolicyStage(d.pop("stage"))

        stage_order = d.pop("stageOrder")

        code = PolicyBlockerCode(d.pop("code"))

        message = d.pop("message")

        policy_blocker = cls(
            stage=stage,
            stage_order=stage_order,
            code=code,
            message=message,
        )

        return policy_blocker

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.approval_state import ApprovalState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="ActionSummary")


@_attrs_define
class ActionSummary:
    """One row of the actions queue (PD-3 item 5) — an approval card, unexpanded.

    Attributes:
        id (UUID):
        recommendation_id (UUID):
        version (int):
        state (ApprovalState): One node of the §8.4 approval state machine. The set is closed; it is the authoritative
            lifecycle vocabulary for a card and its history.
        price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1). Value =
            mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
            representable because the account's entry currency is known; it stays excluded from executable paths until
            S16+S35.
        expires_at (datetime.datetime):
        idempotency_key (str | Unset):
        created_at (datetime.datetime | Unset):
    """

    id: UUID
    recommendation_id: UUID
    version: int
    state: ApprovalState
    price: MoneyAmount
    expires_at: datetime.datetime
    idempotency_key: str | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        recommendation_id = str(self.recommendation_id)

        version = self.version

        state = self.state.value

        price = self.price.to_dict()

        expires_at = self.expires_at.isoformat()

        idempotency_key = self.idempotency_key

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "recommendationId": recommendation_id,
                "version": version,
                "state": state,
                "price": price,
                "expiresAt": expires_at,
            }
        )
        if idempotency_key is not UNSET:
            field_dict["idempotencyKey"] = idempotency_key
        if created_at is not UNSET:
            field_dict["createdAt"] = created_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        recommendation_id = UUID(d.pop("recommendationId"))

        version = d.pop("version")

        state = ApprovalState(d.pop("state"))

        price = MoneyAmount.from_dict(d.pop("price"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        idempotency_key = d.pop("idempotencyKey", UNSET)

        _created_at = d.pop("createdAt", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        action_summary = cls(
            id=id,
            recommendation_id=recommendation_id,
            version=version,
            state=state,
            price=price,
            expires_at=expires_at,
            idempotency_key=idempotency_key,
            created_at=created_at,
        )

        return action_summary

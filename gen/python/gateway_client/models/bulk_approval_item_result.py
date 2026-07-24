from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.bulk_approval_item_state import BulkApprovalItemState
from ..models.selection_set_disposition import SelectionSetDisposition

T = TypeVar("T", bound="BulkApprovalItemResult")


@_attrs_define
class BulkApprovalItemResult:
    """One selection-set member's authoritative bulk outcome. `disposition` is the SERVER-sealed disposition of the bound
    (immutable) version — never a client assertion.

        Attributes:
            variant_id (UUID):
            recommendation_id (UUID):
            disposition (SelectionSetDisposition): A selection-set member's bulk disposition (CHAT-050).
            state (BulkApprovalItemState): A per-member bulk-confirmation outcome (issue #90). Only `authorized` and
                `already_authorized` mean the member carries a durable authorization + execution intent; every other state means
                the member did NOT execute this call. `failed` is a TRANSIENT failure a resume (re-confirm) retries; the other
                terminal states are not retried into execution.
                BULK-PROTOCOL DESIGN RECORD (c) — IDEMPOTENCY-KEY SCHEME. A bulk confirmation mints NO bulk-specific idempotency
                key. Each member is authorized through the SAME §8.4 individual-confirm path, so the durable idempotency key is
                the MEMBER CARD's own key — derived from its APR-001 binding (action id + parameter version + context version +
                policy / cost-profile / evidence versions), unique per card, and the same key the individual confirmation and
                the downstream execution use. The durable execution intent is unique by card id, so a replayed bulk confirmation
                collapses to at most ONE authorization and ONE intent per member. A resume is therefore safe by construction and
                requires no client-supplied request id: re-confirming the same (lineage, version) pair re-derives the same per-
                member keys.
                `already_authorized` is the SEALED authorization outcome: it is reported for a member whose control was
                activated by a prior confirmation, INCLUDING one whose card has since advanced downstream (revalidating,
                executing, or a terminal external result — accepted, rejected, pending_reconciliation, failed). Re-attempting a
                member whose EXECUTION failed is the reconciliation-gated retry path's decision (an unknown result must
                reconcile first; only a definitively reconciled failure is retry-eligible, EXE-003), never a second bulk
                authorization.
            reason (str): A stable, non-localized diagnostic reason for the item's state.
    """

    variant_id: UUID
    recommendation_id: UUID
    disposition: SelectionSetDisposition
    state: BulkApprovalItemState
    reason: str

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        recommendation_id = str(self.recommendation_id)

        disposition = self.disposition.value

        state = self.state.value

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "recommendationId": recommendation_id,
                "disposition": disposition,
                "state": state,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        recommendation_id = UUID(d.pop("recommendationId"))

        disposition = SelectionSetDisposition(d.pop("disposition"))

        state = BulkApprovalItemState(d.pop("state"))

        reason = d.pop("reason")

        bulk_approval_item_result = cls(
            variant_id=variant_id,
            recommendation_id=recommendation_id,
            disposition=disposition,
            state=state,
            reason=reason,
        )

        return bulk_approval_item_result

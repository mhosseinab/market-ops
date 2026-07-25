from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.bulk_approval_item_state import BulkApprovalItemState
from ..models.selection_set_disposition import SelectionSetDisposition
from ..types import UNSET, Unset

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
                the member did NOT execute this call. `failed` means this call neither authorized the member nor voided an
                authorization: either a TRANSIENT failure (the authorization rolled back, the card is still a live control) or
                an outcome that could not be DETERMINED (its state re-read failed). Both are resume-safe — a re-confirm retries
                the live control and re-derives an undetermined outcome. A member that a CONCURRENT confirmation durably
                approved is never `failed`; it is `already_authorized`. The other terminal states are not retried into
                execution.
                BULK-PROTOCOL DESIGN RECORD (c) — IDEMPOTENCY-KEY SCHEME. A bulk confirmation mints NO bulk-specific idempotency
                key. Each member is authorized through the SAME §8.4 individual-confirm path, so the durable idempotency key is
                the MEMBER CARD's own key — derived from its APR-001 binding (action id + parameter version + context version +
                policy / cost-profile / evidence versions), unique per card, and the same key the individual confirmation and
                the downstream execution use. The durable execution intent is unique by card id, so a replayed bulk confirmation
                collapses to at most ONE authorization and ONE intent per member. A resume is therefore safe by construction and
                requires no client-supplied request id: re-confirming the same (lineage, version) pair re-derives the same per-
                member keys.
                `already_authorized` is the SEALED authorization outcome: it is reported for a member whose control was
                activated by ANOTHER confirmation — a prior one (a resume) or a CONCURRENT one that committed first (a double-
                clicked confirm, or a client retry of a confirmation whose response was lost) — INCLUDING one whose card has
                since advanced downstream (revalidating, executing, or a terminal external result — accepted, rejected,
                pending_reconciliation, failed). The member state behind this outcome is read FRESH at report time, so a member
                the race durably approved reports a sealed authorization with `executionPending` true, never a failure. Re-
                attempting a member whose EXECUTION failed is the reconciliation-gated retry path's decision (an unknown result
                must reconcile first; only a definitively reconciled failure is retry-eligible, EXE-003), never a second bulk
                authorization.
            reason (str): A stable, non-localized diagnostic reason for the item's state. `variant_reservation_held` reports
                BULK-PROTOCOL DESIGN RECORD (a): another card already holds the durable (account, variant) execution
                reservation, so this member was NOT dispatched; it is resume-safe and authorizes once the holder reaches a
                definite external result. `authorized_outside_selection` reports that the member's control was activated by an
                INDIVIDUAL confirmation or a DIFFERENT selection set, so this selection may not claim it (issue #87).
            offer_identity (str | Unset): The SERVER-SEALED observed-offer identity of this member, read from the sealed
                member row of the BOUND selection-set version — the SAME value the preview reported (issue #87 criterion D). It
                is never re-derived by a lookup-by-target at confirm time, which could resolve a different sibling offer if the
                target's observations changed after the operator reviewed the preview.
                An empty string is EXPLICIT ABSENCE, never a stand-in for another offer. ADDITIVE and OPTIONAL: it is not in
                `required`, and it is never a client assertion.
    """

    variant_id: UUID
    recommendation_id: UUID
    disposition: SelectionSetDisposition
    state: BulkApprovalItemState
    reason: str
    offer_identity: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        recommendation_id = str(self.recommendation_id)

        disposition = self.disposition.value

        state = self.state.value

        reason = self.reason

        offer_identity = self.offer_identity

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
        if offer_identity is not UNSET:
            field_dict["offerIdentity"] = offer_identity

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        recommendation_id = UUID(d.pop("recommendationId"))

        disposition = SelectionSetDisposition(d.pop("disposition"))

        state = BulkApprovalItemState(d.pop("state"))

        reason = d.pop("reason")

        offer_identity = d.pop("offerIdentity", UNSET)

        bulk_approval_item_result = cls(
            variant_id=variant_id,
            recommendation_id=recommendation_id,
            disposition=disposition,
            state=state,
            reason=reason,
            offer_identity=offer_identity,
        )

        return bulk_approval_item_result

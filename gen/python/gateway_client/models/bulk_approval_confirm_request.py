from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="BulkApprovalConfirmRequest")


@_attrs_define
class BulkApprovalConfirmRequest:
    """A bulk approval confirmation bound to ONE exact selection-set version (CHAT-052). The server rejects it when the
    bound version is no longer current (any set/evidence change mints a new version).
    BULK-PROTOCOL DESIGN RECORD (d) — VERSION RANGE / ORDERING. `boundVersion` is meaningful ONLY together with
    `selectionSetLineage`: versions are monotonic WITHIN one lineage and version numbers from different lineages are NOT
    comparable. Never order, range, or diff versions across lineages, and never accept a bare version without its
    lineage — the binding is the PAIR.
    BULK-PROTOCOL DESIGN RECORD (a) — RESERVATION LIFECYCLE. What #90 establishes: binding a (lineage, version) pair
    under the per-lineage lock is the SELECTION reservation — it fixes exactly which members, dispositions and aggregate
    the operator authorized, and it is immutable for that version (#91). Each member is then authorized through its own
    §8.4 individual confirm and its own durable execution intent (unique by card id). What #90 does NOT establish, and
    #87 must add: a durable `(account, variant)` EXECUTION reservation spanning the window between authorization and
    terminal external result, so two different selection sets (or a bulk and an individual confirmation) cannot hold
    concurrent in-flight writes for the same variant. Until #87 lands, concurrency on one variant is bounded only by the
    card-level FROM-guard and the card-id-unique intent — sufficient to prevent a duplicate write for one card, NOT to
    prevent two cards on one variant. #87 owns that reservation's acquire/release/expiry semantics; #90 pins only that
    it is keyed on `(account, variant)` and must be acquired BEFORE dispatch and released on a terminal external result.
    BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY. #87's `offerIdentity` on bulk confirm/item
    results is ADDITIVE and OPTIONAL: it must not enter the `required` set of any existing schema, must not change
    `additionalProperties: false`, and must never be a CLIENT ASSERTION. The server seals the disposition and the offer
    identity from its own persisted observation/recommendation state at preview time; a client-supplied `offerIdentity`
    is a selector to be validated against the sealed value, never an input that can widen or redirect what gets
    authorized. A mismatch fails closed as a uniform not-found, exactly like an unknown member.

        Attributes:
            selection_set_lineage (UUID): The selection-set lineage the preview was built from.
            bound_version (int): The exact selection-set version the preview bound to.
    """

    selection_set_lineage: UUID
    bound_version: int

    def to_dict(self) -> dict[str, Any]:
        selection_set_lineage = str(self.selection_set_lineage)

        bound_version = self.bound_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "selectionSetLineage": selection_set_lineage,
                "boundVersion": bound_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        selection_set_lineage = UUID(d.pop("selectionSetLineage"))

        bound_version = d.pop("boundVersion")

        bulk_approval_confirm_request = cls(
            selection_set_lineage=selection_set_lineage,
            bound_version=bound_version,
        )

        return bulk_approval_confirm_request

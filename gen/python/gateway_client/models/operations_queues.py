from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.parser_drift_queue import ParserDriftQueue
    from ..models.pending_reconciliation_action import PendingReconciliationAction


T = TypeVar("T", bound="OperationsQueues")


@_attrs_define
class OperationsQueues:
    """
    Attributes:
        marketplace_account_id (UUID):
        pending_reconciliation (list[PendingReconciliationAction]):
        parser_drift (ParserDriftQueue): The Route C parser/schema-drift queue. NOT YET backed by a persisted store
            (§10.4) — `available` is false with an explicit reason rather than a fabricated empty success, per the screens-
            only-fallback / unavailable-with-reason posture (PRC-001 optionality). Closing this is a named follow-up on the
            Route C observer plane.
    """

    marketplace_account_id: UUID
    pending_reconciliation: list[PendingReconciliationAction]
    parser_drift: ParserDriftQueue

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        pending_reconciliation = []
        for pending_reconciliation_item_data in self.pending_reconciliation:
            pending_reconciliation_item = pending_reconciliation_item_data.to_dict()
            pending_reconciliation.append(pending_reconciliation_item)

        parser_drift = self.parser_drift.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "pendingReconciliation": pending_reconciliation,
                "parserDrift": parser_drift,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.parser_drift_queue import ParserDriftQueue
        from ..models.pending_reconciliation_action import PendingReconciliationAction

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        pending_reconciliation = []
        _pending_reconciliation = d.pop("pendingReconciliation")
        for pending_reconciliation_item_data in _pending_reconciliation:
            pending_reconciliation_item = PendingReconciliationAction.from_dict(pending_reconciliation_item_data)

            pending_reconciliation.append(pending_reconciliation_item)

        parser_drift = ParserDriftQueue.from_dict(d.pop("parserDrift"))

        operations_queues = cls(
            marketplace_account_id=marketplace_account_id,
            pending_reconciliation=pending_reconciliation,
            parser_drift=parser_drift,
        )

        return operations_queues

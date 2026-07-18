from enum import Enum


class ApprovalState(str, Enum):
    ACCEPTED = "accepted"
    APPROVED = "approved"
    AWAITING_CONFIRMATION = "awaiting_confirmation"
    BLOCKED = "blocked"
    DRAFT = "draft"
    EXECUTING = "executing"
    EXPIRED = "expired"
    FAILED = "failed"
    INVALIDATED = "invalidated"
    PENDING_RECONCILIATION = "pending_reconciliation"
    READY_FOR_REVIEW = "ready_for_review"
    REJECTED = "rejected"
    REVALIDATING = "revalidating"

    def __str__(self) -> str:
        return str(self.value)

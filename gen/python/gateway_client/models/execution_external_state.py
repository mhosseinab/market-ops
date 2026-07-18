from enum import Enum


class ExecutionExternalState(str, Enum):
    ACCEPTED = "accepted"
    FAILED = "failed"
    PENDING_RECONCILIATION = "pending_reconciliation"
    REJECTED = "rejected"

    def __str__(self) -> str:
        return str(self.value)

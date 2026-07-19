from enum import Enum


class BulkApprovalItemState(str, Enum):
    ALREADY_AUTHORIZED = "already_authorized"
    AUTHORIZED = "authorized"
    EXCLUDED = "excluded"
    FAILED = "failed"
    INVALIDATED = "invalidated"

    def __str__(self) -> str:
        return str(self.value)

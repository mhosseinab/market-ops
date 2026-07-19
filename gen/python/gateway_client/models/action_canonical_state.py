from enum import Enum


class ActionCanonicalState(str, Enum):
    AWAITING = "awaiting"
    FAILED = "failed"
    LAPSED = "lapsed"
    REJECTED = "rejected"
    SUCCEEDED = "succeeded"
    UNKNOWN = "unknown"

    def __str__(self) -> str:
        return str(self.value)

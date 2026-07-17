from enum import Enum


class MarginReadinessState(str, Enum):
    COMPLETE = "complete"
    MISSING = "missing"
    PARTIAL = "partial"
    STALE = "stale"

    def __str__(self) -> str:
        return str(self.value)

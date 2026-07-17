from enum import Enum


class EventLifecycleState(str, Enum):
    EXPIRED = "expired"
    OPEN = "open"
    RESOLVED = "resolved"
    UPDATED = "updated"

    def __str__(self) -> str:
        return str(self.value)

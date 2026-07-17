from enum import Enum


class EventSeverity(str, Enum):
    CRITICAL = "critical"
    INFO = "info"
    WARNING = "warning"

    def __str__(self) -> str:
        return str(self.value)

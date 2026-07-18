from enum import Enum


class NotificationCategory(str, Enum):
    EXECUTION_FAILURE = "execution_failure"
    MARKET_EVENT = "market_event"
    SAFETY_FAILURE = "safety_failure"

    def __str__(self) -> str:
        return str(self.value)

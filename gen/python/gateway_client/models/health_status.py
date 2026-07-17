from enum import Enum


class HealthStatus(str, Enum):
    OK = "ok"

    def __str__(self) -> str:
        return str(self.value)

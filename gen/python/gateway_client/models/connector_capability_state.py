from enum import Enum


class ConnectorCapabilityState(str, Enum):
    DEGRADED = "degraded"
    SUPPORTED = "supported"
    UNKNOWN = "unknown"
    UNSUPPORTED = "unsupported"

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class QualityState(str, Enum):
    CONFLICTED = "conflicted"
    STALE = "stale"
    SUPPORTED = "supported"
    UNAVAILABLE = "unavailable"
    UNVERIFIED = "unverified"
    VERIFIED = "verified"

    def __str__(self) -> str:
        return str(self.value)

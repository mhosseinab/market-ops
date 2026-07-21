from enum import Enum


class LatestBriefingReadProvenance(str, Enum):
    NONE = "none"
    STORED_BRIEFING = "stored_briefing"

    def __str__(self) -> str:
        return str(self.value)

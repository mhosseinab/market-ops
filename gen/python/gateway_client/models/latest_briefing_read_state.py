from enum import Enum


class LatestBriefingReadState(str, Enum):
    AVAILABLE = "available"
    NEVER_GENERATED = "never_generated"

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class PolicyStrategy(str, Enum):
    HOLD = "hold"
    MATCH = "match"
    UNDERCUT = "undercut"

    def __str__(self) -> str:
        return str(self.value)

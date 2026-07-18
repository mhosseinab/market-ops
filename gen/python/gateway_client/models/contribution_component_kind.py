from enum import Enum


class ContributionComponentKind(str, Enum):
    ABSOLUTE = "absolute"
    RATE = "rate"

    def __str__(self) -> str:
        return str(self.value)

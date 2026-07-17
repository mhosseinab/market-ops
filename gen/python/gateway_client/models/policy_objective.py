from enum import Enum


class PolicyObjective(str, Enum):
    MAXIMIZE_CONTRIBUTION = "maximize_contribution"
    TRACK_STRATEGY = "track_strategy"

    def __str__(self) -> str:
        return str(self.value)

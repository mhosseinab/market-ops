from enum import Enum


class PolicyStage(str, Enum):
    BOUNDARY = "boundary"
    COOLDOWN = "cooldown"
    HARD_FLOOR = "hard_floor"
    MOVEMENT_CAP = "movement_cap"
    OBJECTIVE = "objective"
    STRATEGY = "strategy"

    def __str__(self) -> str:
        return str(self.value)

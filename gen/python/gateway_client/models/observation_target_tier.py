from enum import Enum


class ObservationTargetTier(str, Enum):
    BACKGROUND = "background"
    PRIORITY = "priority"
    STANDARD = "standard"

    def __str__(self) -> str:
        return str(self.value)

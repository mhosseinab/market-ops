from enum import Enum


class EventRelevanceKind(str, Enum):
    MUTED = "muted"
    NOT_RELEVANT = "not_relevant"
    RELEVANT = "relevant"

    def __str__(self) -> str:
        return str(self.value)

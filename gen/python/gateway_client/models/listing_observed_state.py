from enum import Enum


class ListingObservedState(str, Enum):
    EMPTY = "empty"
    NOT_OBSERVED = "not_observed"
    PRESENT = "present"

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class CatalogMappingState(str, Enum):
    CONFIRMED = "confirmed"
    NEEDS_REVIEW = "needs_review"
    OBSOLETE = "obsolete"
    REJECTED = "rejected"
    UNMAPPED = "unmapped"

    def __str__(self) -> str:
        return str(self.value)

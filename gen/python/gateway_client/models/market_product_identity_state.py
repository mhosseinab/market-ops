from enum import Enum


class MarketProductIdentityState(str, Enum):
    CONFIRMED = "confirmed"
    NEEDS_REVIEW = "needs_review"
    OBSOLETE = "obsolete"
    REJECTED = "rejected"

    def __str__(self) -> str:
        return str(self.value)

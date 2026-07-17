from enum import Enum


class EventType(str, Enum):
    COMPETITOR_PRICE = "competitor_price"
    CONTRIBUTION_FLOOR = "contribution_floor"
    SELLER_COUNT = "seller_count"
    SUPPRESSION_BOUNDARY = "suppression_boundary"
    WINNING_STATE = "winning_state"

    def __str__(self) -> str:
        return str(self.value)

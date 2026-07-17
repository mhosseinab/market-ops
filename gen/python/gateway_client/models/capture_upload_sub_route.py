from enum import Enum


class CaptureUploadSubRoute(str, Enum):
    ON_DEMAND = "on_demand"
    PASSIVE = "passive"
    WATCHLIST = "watchlist"

    def __str__(self) -> str:
        return str(self.value)

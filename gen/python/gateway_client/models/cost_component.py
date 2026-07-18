from enum import Enum


class CostComponent(str, Enum):
    ADS = "ads"
    COGS = "cogs"
    COMMISSION = "commission"
    FULFILLMENT = "fulfillment"
    PACKAGING = "packaging"
    PROMOTION = "promotion"
    RETURNS = "returns"
    SHIPPING = "shipping"

    def __str__(self) -> str:
        return str(self.value)

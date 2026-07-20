from enum import Enum


class ListingDiagnosticEntity(str, Enum):
    LISTING = "listing"
    PRODUCT = "product"
    VARIANT = "variant"

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class CaptureUploadAvailabilityStatus(str, Enum):
    IN_STOCK = "in_stock"
    LIMITED = "limited"
    OUT_OF_STOCK = "out_of_stock"
    UNAVAILABLE = "unavailable"

    def __str__(self) -> str:
        return str(self.value)

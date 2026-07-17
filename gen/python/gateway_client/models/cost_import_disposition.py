from enum import Enum


class CostImportDisposition(str, Enum):
    ACCEPT = "accept"
    DUPLICATE = "duplicate"
    REJECT = "reject"

    def __str__(self) -> str:
        return str(self.value)

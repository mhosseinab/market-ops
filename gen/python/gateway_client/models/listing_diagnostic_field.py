from enum import Enum


class ListingDiagnosticField(str, Enum):
    DESCRIPTION = "description"
    IMAGE = "image"
    TITLE = "title"

    def __str__(self) -> str:
        return str(self.value)

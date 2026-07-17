from enum import Enum


class CostImportPreviewStatus(str, Enum):
    CANCELLED = "cancelled"
    COMMITTED = "committed"
    PREVIEW = "preview"

    def __str__(self) -> str:
        return str(self.value)

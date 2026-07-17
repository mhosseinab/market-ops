from enum import Enum


class CostImportCommitResultStatus(str, Enum):
    COMMITTED = "committed"

    def __str__(self) -> str:
        return str(self.value)

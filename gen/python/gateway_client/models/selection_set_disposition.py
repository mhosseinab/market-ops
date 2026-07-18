from enum import Enum


class SelectionSetDisposition(str, Enum):
    BLOCKED = "blocked"
    EXECUTABLE = "executable"
    WARNING = "warning"

    def __str__(self) -> str:
        return str(self.value)

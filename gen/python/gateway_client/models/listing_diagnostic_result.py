from enum import Enum


class ListingDiagnosticResult(str, Enum):
    PASS = "pass"
    WARN = "warn"

    def __str__(self) -> str:
        return str(self.value)

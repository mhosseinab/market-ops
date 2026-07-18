from enum import Enum


class CostProfileVersionSource(str, Enum):
    CONNECTOR = "connector"
    CSV_IMPORT = "csv_import"
    SINGLE_VALUE = "single_value"

    def __str__(self) -> str:
        return str(self.value)

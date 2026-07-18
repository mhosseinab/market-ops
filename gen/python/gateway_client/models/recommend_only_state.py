from enum import Enum


class RecommendOnlyState(str, Enum):
    AWAITING_EXTERNAL_EXECUTION = "awaiting_external_execution"
    EXTERNALLY_EXECUTED = "externally_executed"
    LAPSED = "lapsed"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class ExecutionMode(str, Enum):
    RECOMMEND_ONLY = "recommend_only"
    WRITE = "write"

    def __str__(self) -> str:
        return str(self.value)

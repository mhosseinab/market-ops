from enum import Enum


class CatalogSyncState(str, Enum):
    COMPLETED = "completed"
    FAILED = "failed"
    NONE = "none"
    QUEUED = "queued"
    RUNNING = "running"

    def __str__(self) -> str:
        return str(self.value)

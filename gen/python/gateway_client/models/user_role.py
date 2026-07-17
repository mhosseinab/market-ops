from enum import Enum


class UserRole(str, Enum):
    INTERNAL = "internal"
    OPERATOR = "operator"
    OWNER = "owner"

    def __str__(self) -> str:
        return str(self.value)

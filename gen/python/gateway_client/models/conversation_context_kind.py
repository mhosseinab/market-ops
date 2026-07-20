from enum import Enum


class ConversationContextKind(str, Enum):
    ACTION = "action"
    BULK = "bulk"
    EVENT = "event"
    GLOBAL = "global"
    OPERATIONS = "operations"
    PRODUCT = "product"
    RECOMMENDATION = "recommendation"
    SETTINGS = "settings"

    def __str__(self) -> str:
        return str(self.value)

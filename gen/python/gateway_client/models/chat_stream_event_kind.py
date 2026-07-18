from enum import Enum


class ChatStreamEventKind(str, Enum):
    CONVERSATION = "conversation"
    FAILURE = "failure"
    FINAL = "final"
    TOKEN = "token"

    def __str__(self) -> str:
        return str(self.value)

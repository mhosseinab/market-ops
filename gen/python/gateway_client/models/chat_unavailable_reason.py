from enum import Enum


class ChatUnavailableReason(str, Enum):
    KILL_SWITCH_ACCOUNT = "kill_switch_account"
    KILL_SWITCH_GLOBAL = "kill_switch_global"
    PROVIDER_UNAVAILABLE = "provider_unavailable"

    def __str__(self) -> str:
        return str(self.value)

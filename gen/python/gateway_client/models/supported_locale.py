from enum import Enum


class SupportedLocale(str, Enum):
    EN = "en"
    FA_IR = "fa-IR"

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class CaptureUploadSourceType(str, Enum):
    DOM = "dom"
    EMBEDDED_JSON = "embedded-json"
    PUBLIC_WEB_ENDPOINT = "public-web-endpoint"
    USER_TRIGGERED_REQUEST = "user-triggered-request"

    def __str__(self) -> str:
        return str(self.value)

from enum import Enum


class CaptureUploadConfidence(str, Enum):
    PARTIALLY_VERIFIED = "partially_verified"
    UNVERIFIED = "unverified"
    VERIFIED = "verified"

    def __str__(self) -> str:
        return str(self.value)

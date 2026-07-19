from enum import Enum


class OwnedOfferUnavailableReason(str, Enum):
    CAPABILITY_NOT_SUPPORTED = "capability_not_supported"
    NO_OWNED_OFFER = "no_owned_offer"

    def __str__(self) -> str:
        return str(self.value)

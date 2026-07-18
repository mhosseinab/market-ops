from enum import Enum


class OutcomeSummaryResult(str, Enum):
    INCONCLUSIVE = "inconclusive"
    NEGATIVE = "negative"
    NEUTRAL = "neutral"
    NOT_MEASURABLE = "not_measurable"
    POSITIVE = "positive"

    def __str__(self) -> str:
        return str(self.value)

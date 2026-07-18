from enum import Enum


class ExecutionGate(str, Enum):
    BOUNDARY = "boundary"
    COSTS = "costs"
    CURRENT_PRICE = "current_price"
    EVIDENCE_JIT = "evidence_jit"
    EXPIRY = "expiry"
    GUARDRAILS = "guardrails"
    IDENTITY = "identity"
    MONEY_UNIT = "money_unit"
    PERMISSION = "permission"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)

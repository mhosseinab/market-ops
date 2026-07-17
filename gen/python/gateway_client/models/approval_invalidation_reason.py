from enum import Enum


class ApprovalInvalidationReason(str, Enum):
    ACTION_MISMATCH = "action_mismatch"
    CONTEXT_VERSION_CHANGED = "context_version_changed"
    COST_VERSION_CHANGED = "cost_version_changed"
    EVIDENCE_VERSION_CHANGED = "evidence_version_changed"
    EXPIRED = "expired"
    PARAMETER_VERSION_CHANGED = "parameter_version_changed"
    POLICY_VERSION_CHANGED = "policy_version_changed"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)

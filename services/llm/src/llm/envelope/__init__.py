"""Typed envelope models for the LLM plane (PRD §12.2, §9.1).

Money NEVER uses float inside the LLM plane (§9.1 holds here too): it carries
``mantissa`` / ``currency`` / ``exponent`` or a raw evidence string. The final
response envelope separates category-classified content (§12.2). S20 shipped the
money-safe primitives, the §12.4 structured failure, and the SSE stream-event
shapes; S22 adds the full §12.2 category-separated :class:`ResponseEnvelope`, the
grounding walker that rejects an ungrounded response, and the composer that
places model text only in the inference slot and fails closed on any violation.
"""

from llm.envelope.composer import (
    CANNOT_ANSWER_REASON_KEY,
    FALLBACK_DEEP_LINK,
    compose,
    compose_or_refuse,
    fail_closed,
)
from llm.envelope.contract import (
    MAX_INLINE_ROWS,
    AvailabilityCatalog,
    Calculation,
    CannotAnswer,
    Claim,
    Comparison,
    ExposureTotal,
    InlineTable,
    Provenance,
    Recommendation,
    ResponseEnvelope,
    SectionScope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.grounding import (
    CANONICAL_QUALITY_KEYS,
    CANONICAL_STATE_KEYS,
    GroundingError,
    Violation,
    find_violations,
    validate_grounding,
)
from llm.envelope.models import (
    AssistantAnswer,
    ChatStreamEvent,
    EvidenceRef,
    Money,
    RawEvidenceValue,
    StreamEventKind,
    TurnFailure,
)

__all__ = [
    "CANNOT_ANSWER_REASON_KEY",
    "CANONICAL_QUALITY_KEYS",
    "CANONICAL_STATE_KEYS",
    "FALLBACK_DEEP_LINK",
    "MAX_INLINE_ROWS",
    "AssistantAnswer",
    "AvailabilityCatalog",
    "Calculation",
    "CannotAnswer",
    "ChatStreamEvent",
    "Claim",
    "Comparison",
    "EvidenceRef",
    "ExposureTotal",
    "GroundingError",
    "InlineTable",
    "Money",
    "Provenance",
    "RawEvidenceValue",
    "Recommendation",
    "ResponseEnvelope",
    "SectionScope",
    "SourceRef",
    "SourcedValue",
    "StreamEventKind",
    "TurnFailure",
    "Violation",
    "compose",
    "compose_or_refuse",
    "fail_closed",
    "find_violations",
    "validate_grounding",
]

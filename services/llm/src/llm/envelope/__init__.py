"""Typed envelope models for the LLM plane (PRD §12.2, §9.1).

Money NEVER uses float inside the LLM plane (§9.1 holds here too): it carries
``mantissa`` / ``currency`` / ``exponent`` or a raw evidence string. The final
response envelope separates category-classified content (§12.2); the response
contract's full validation lands in S22, so S20 ships the money-safe primitives,
the §12.4 structured failure, and the SSE stream-event shapes the app emits.
"""

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
    "AssistantAnswer",
    "ChatStreamEvent",
    "EvidenceRef",
    "Money",
    "RawEvidenceValue",
    "StreamEventKind",
    "TurnFailure",
]

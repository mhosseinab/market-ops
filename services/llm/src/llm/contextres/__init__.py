"""Deterministic context resolver (PRD §8.1, CHAT-007). NO model in the loop.

Exactly one context is active per conversation (§8.1). This package resolves the
active context for a turn as PURE functions of the turn's inputs — never a model
call, never a guess:

* an explicit entity reference overrides a compatible active context;
* ambiguity that could lead to a card yields a structured PICKER, never a guess
  (CHAT-007 — zero ambiguous cases create a card directly);
* time-range resolution always yields an explicit range plus an as-of time
  (§8.1: time-range answers display range and as-of).
"""

from __future__ import annotations

from llm.contextres.models import (
    CARD_CAPABLE_CONTEXTS,
    CARD_LEADING_INTENTS,
    ContextChip,
    ContextType,
    EntityCandidate,
    EntityRef,
    PickerOption,
    RequestScope,
    Resolution,
    ResolutionKind,
    ResolveRequest,
    TimeRange,
    missing_card_version_reason,
    scope_mismatch_reason,
)
from llm.contextres.resolver import resolve, resolve_time_range

__all__ = [
    "CARD_CAPABLE_CONTEXTS",
    "CARD_LEADING_INTENTS",
    "ContextChip",
    "ContextType",
    "EntityCandidate",
    "EntityRef",
    "PickerOption",
    "RequestScope",
    "Resolution",
    "ResolutionKind",
    "ResolveRequest",
    "TimeRange",
    "missing_card_version_reason",
    "resolve",
    "resolve_time_range",
    "scope_mismatch_reason",
]

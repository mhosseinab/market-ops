"""Intent classification and deterministic routing (PRD §8.2, §12.1, CHAT-080/081).

The small model classifies a turn into exactly one of the eight intent classes;
routing from a class to a *disposition* is then a PURE, deterministic function —
the model never decides whether a tool may run. Two classes, ApproveAction and
ConfirmResult, are structurally GUIDANCE_ONLY: they NEVER route to any tool and
instead point the user at the structured control that lives outside the model
plane (free text never approves — PRD §8, §12.3, CHAT-041).
"""

from __future__ import annotations

from llm.intents.capabilities import (
    EMPTY_CAPABILITY,
    IntentCapability,
    capability_for,
)
from llm.intents.classifier import IntentClassifier, IntentDecision
from llm.intents.models import (
    GUIDANCE_ONLY_INTENTS,
    IntentClass,
    IntentClassification,
    IntentDisposition,
    IntentRoute,
    route_intent,
)
from llm.intents.normalize import NormalizedInput, normalize_digits, normalize_input, tokenize

__all__ = [
    "EMPTY_CAPABILITY",
    "GUIDANCE_ONLY_INTENTS",
    "IntentCapability",
    "IntentClass",
    "IntentClassification",
    "IntentClassifier",
    "IntentDecision",
    "IntentDisposition",
    "IntentRoute",
    "NormalizedInput",
    "capability_for",
    "normalize_digits",
    "normalize_input",
    "route_intent",
    "tokenize",
]

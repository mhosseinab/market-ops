"""Context-resolution types (PRD §8.1, CHAT-007).

All types are JSON-safe Pydantic / enums so an eval fixture maps 1:1 onto a
:class:`ResolveRequest` and the resolver output is comparable field-for-field.
The resolver (``resolver.py``) is a pure function over these — no model, no I/O.
"""

from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field

from llm.intents.models import IntentClass


class ContextType(StrEnum):
    """The eight context chips (PRD §8.1). Exactly one is active per turn."""

    GLOBAL_ACCOUNT = "GlobalAccount"
    PRODUCT = "Product"
    MARKET_EVENT = "MarketEvent"
    RECOMMENDATION = "Recommendation"
    BULK_SELECTION = "BulkSelection"
    ACTION_EXECUTION = "ActionExecution"
    SETTINGS = "Settings"
    OPERATIONS = "Operations"


# Intents whose satisfaction could create/advance a card (PRD §8.2/§8.4). For
# these, an ambiguous or missing target must produce a PICKER — never a guessed
# card (CHAT-007). Approve is included: its guidance references a specific card,
# so an ambiguous target still pickers rather than guessing which card.
CARD_LEADING_INTENTS: frozenset[IntentClass] = frozenset(
    {
        IntentClass.PREPARE_ACTION,
        IntentClass.REVIEW_ACTION,
        IntentClass.APPROVE_ACTION,
    }
)

# Context types that ARE a specific card-capable target. A card-leading intent
# resolved against one of these has an unambiguous subject; against an
# account-level context (GlobalAccount/Operations/Settings/MarketEvent) it does
# not, so it pickers instead of inventing a subject.
CARD_CAPABLE_CONTEXTS: frozenset[ContextType] = frozenset(
    {
        ContextType.PRODUCT,
        ContextType.RECOMMENDATION,
        ContextType.BULK_SELECTION,
        ContextType.ACTION_EXECUTION,
    }
)


class EntityRef(BaseModel):
    """An entity a reference could resolve to (a candidate or an explicit ref).

    ``context_type`` is the chip a card of this entity would activate; ``raw`` is
    the verbatim token from the message (evidence, LTR-isolated by the UI).
    """

    model_config = ConfigDict(extra="forbid")

    context_type: ContextType
    entity_id: str
    raw: str
    label: str = ""


class EntityCandidate(BaseModel):
    """An authoritative candidate for an explicit reference (PRD §8.1).

    Unlike :class:`EntityRef` (which is only what the message named), a candidate
    is supplied by a deterministic read tool and therefore carries the provenance
    a card binds at creation: the owning ``account_id`` plus the ``context_version``
    (and, for a Recommendation, the ``recommendation_version``) required for
    stale-card invalidation. These survive resolution byte-for-byte; a card-leading
    intent resolving a candidate that lacks a required version fails closed rather
    than emit a chip that cannot be bound or invalidated.
    """

    model_config = ConfigDict(extra="forbid")

    context_type: ContextType
    entity_id: str
    raw: str
    label: str = ""
    account_id: str | None = None
    context_version: str | None = None
    recommendation_version: str | None = None


def missing_card_version_reason(
    context_type: ContextType,
    context_version: str | None,
    recommendation_version: str | None,
) -> str | None:
    """Return a stable reason token if a card-binding version is absent, else None.

    A card-capable context needs ``context_version`` for stale-card invalidation
    (PRD §8.1); a Recommendation additionally needs ``recommendation_version``.
    Non-card-capable contexts bind no card and require neither. The empty string
    counts as absent — a version must be a real, bindable identifier.
    """
    if context_type not in CARD_CAPABLE_CONTEXTS:
        return None
    if not context_version:
        return "missing_context_version"
    if context_type is ContextType.RECOMMENDATION and not recommendation_version:
        return "missing_recommendation_version"
    return None


class ContextChip(BaseModel):
    """The single active context chip, with the identifiers a card binds at
    creation (PRD §8.1: resolved entity, account, context version, recommendation
    version).
    """

    model_config = ConfigDict(extra="forbid")

    context_type: ContextType
    account_id: str | None = None
    entity_id: str | None = None
    context_version: str | None = None
    recommendation_version: str | None = None


class TimeRange(BaseModel):
    """An explicit, closed time range plus the as-of instant (PRD §8.1).

    All three are RFC 3339 UTC strings; a historical range never reads as
    current. ``label`` is a catalog key for the phrase (localization boundary).
    """

    model_config = ConfigDict(extra="forbid")

    start: str
    end: str
    as_of: str
    label_key: str


class PickerOption(BaseModel):
    """One structured option in an ambiguity picker (CHAT-007). Never executable."""

    model_config = ConfigDict(extra="forbid")

    context_type: ContextType
    entity_id: str
    label: str


class ResolutionKind(StrEnum):
    """The three deterministic outcomes of context resolution.

    ``RESOLVED`` — exactly one active chip. ``PICKER`` — ambiguity that could lead
    to a card; the user picks, we never guess. ``NOT_FOUND`` — a reference matched
    nothing; fail closed rather than invent a subject.
    """

    RESOLVED = "resolved"
    PICKER = "picker"
    NOT_FOUND = "not_found"


class Resolution(BaseModel):
    """The resolver's outcome for one turn.

    Exactly one of ``chip`` (RESOLVED) or ``options`` (PICKER) is meaningful;
    ``time_range`` is set whenever the turn carried a time phrase. ``reason`` is a
    stable machine token for tests/telemetry, not user copy.
    """

    model_config = ConfigDict(extra="forbid")

    kind: ResolutionKind
    chip: ContextChip | None = None
    options: list[PickerOption] = Field(default_factory=list)
    time_range: TimeRange | None = None
    reason: str = ""


class ResolveRequest(BaseModel):
    """The full, JSON-safe input to :func:`~llm.contextres.resolver.resolve`.

    ``candidates`` maps each explicit reference's ``raw`` token to the entities it
    could denote (supplied by the caller from deterministic read tools — the
    resolver never fetches). ``now`` is the injected as-of clock so time
    resolution is deterministic and testable.
    """

    model_config = ConfigDict(extra="forbid")

    intent: IntentClass
    account_id: str | None = None
    active_context: ContextChip | None = None
    references: list[EntityRef] = Field(default_factory=list)
    candidates: dict[str, list[EntityCandidate]] = Field(default_factory=dict)
    time_phrase: str | None = None
    now: str = "1970-01-01T00:00:00Z"

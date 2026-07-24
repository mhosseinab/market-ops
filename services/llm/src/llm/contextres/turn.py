"""The typed turn-context payload the gateway puts on a ``/chat`` turn (§8.1).

The Go gateway is the sole authoritative producer of a turn's bound context
(``services/core/internal/httpapi/chat.go``): it resolves the single active chip
from the conversation binding and passes it through as read-only business data.
This module is the LLM plane's typed reader for that payload.

Two shapes meet here and are deliberately kept apart:

* :class:`ContextKind` — the WIRE enum (``product``, ``event``, …), identical to
  the gateway's ``ConversationContextKind`` and to the web dock's
  ``ChatContextKind``. It is what a client sends and what a picker option echoes.
* :class:`~llm.contextres.models.ContextType` — the DOMAIN enum the deterministic
  resolver works in. :meth:`ContextKind.to_context_type` /
  :func:`context_kind_for` are the total, round-tripping bijection between them.

**Tenant rule (PRD §12, §4.6 identity quarantine).** ``organization_id`` /
``account_id`` on this payload are *client-supplied data to be validated*, never
the turn's scope. The authenticated scope of a turn comes from the request's own
identity fields; the resolver checks this payload against it via
:func:`~llm.contextres.models.scope_mismatch_reason` and fails closed on a
missing or foreign tenant. Provenance is never manufactured from the request.

``extra="forbid"``: an unknown or misspelled key inside the context payload is
REJECTED, not silently dropped. Dropping it would silently lose the turn's
subject and let a downstream flow run against the wrong (or no) entity — exactly
the failure this payload exists to prevent. Contract growth here is additive and
lands with the producer change.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Any
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from pydantic import BaseModel, ConfigDict, Field, field_validator

from llm.contextres.models import ContextChip, ContextType, EntityRef

__all__ = ["ContextKind", "TurnContext", "context_kind_for"]

# The most explicit references one turn may carry. A bounded input: the resolver
# pickers on more than one anyway, so a large list only wastes work.
MAX_TURN_REFERENCES = 8
# A time phrase is a short natural-language span, never a payload channel.
MAX_TIME_PHRASE_LENGTH = 200


class ContextKind(StrEnum):
    """The eight wire context kinds (gateway ``ConversationContextKind``)."""

    GLOBAL = "global"
    PRODUCT = "product"
    EVENT = "event"
    RECOMMENDATION = "recommendation"
    BULK = "bulk"
    ACTION = "action"
    SETTINGS = "settings"
    OPERATIONS = "operations"

    def to_context_type(self) -> ContextType:
        """The domain context type this wire kind denotes. Total."""
        return _KIND_TO_TYPE[self]


_KIND_TO_TYPE: dict[ContextKind, ContextType] = {
    ContextKind.GLOBAL: ContextType.GLOBAL_ACCOUNT,
    ContextKind.PRODUCT: ContextType.PRODUCT,
    ContextKind.EVENT: ContextType.MARKET_EVENT,
    ContextKind.RECOMMENDATION: ContextType.RECOMMENDATION,
    ContextKind.BULK: ContextType.BULK_SELECTION,
    ContextKind.ACTION: ContextType.ACTION_EXECUTION,
    ContextKind.SETTINGS: ContextType.SETTINGS,
    ContextKind.OPERATIONS: ContextType.OPERATIONS,
}

_TYPE_TO_KIND: dict[ContextType, ContextKind] = {v: k for k, v in _KIND_TO_TYPE.items()}


def context_kind_for(context_type: ContextType) -> ContextKind:
    """The wire kind for a domain context type. Total (the map is a bijection)."""
    return _TYPE_TO_KIND[context_type]


def _as_identifier(value: Any, field: str) -> str | None:  # noqa: ANN401
    """Normalize a version/identifier to its string wire form, or fail closed.

    A version identifier arrives as a JSON string or a JSON integer depending on
    the producer. Both are accepted and held as a string (the shape
    :class:`~llm.contextres.models.ContextChip` binds); ``bool``/``float`` are
    type errors, never coerced (§4.6 quarantine over inference).
    """
    if value is None:
        return None
    if isinstance(value, bool):
        raise ValueError(f"{field} must be a string or integer identifier, not bool")
    if isinstance(value, int):
        return str(value)
    if isinstance(value, str):
        return value
    raise ValueError(f"{field} must be a string or integer identifier")


class TurnContext(BaseModel):
    """The authoritative context a single ``/chat`` turn carries.

    Everything the deterministic resolver needs that the gateway can supply:
    the single active chip (``kind`` + its bound identifiers), the explicit
    entity references the turn named, the turn's time phrase, and the account's
    calendar DATA (timezone / week start — never a locale branch, PRD §11).

    Candidates are deliberately NOT on the wire: they are authoritative read
    results and arrive through :class:`~llm.contextres.ports.CandidatePort`.
    """

    model_config = ConfigDict(extra="forbid")

    kind: ContextKind
    entity_id: str | None = Field(default=None, max_length=200)
    version: str | None = None
    recommendation_version: str | None = None
    # Client-supplied PROVENANCE — validated against the authenticated request
    # scope by the resolver, never used as the scope itself (PRD §12).
    organization_id: str | None = Field(default=None, max_length=200)
    account_id: str | None = Field(default=None, max_length=200)
    references: list[EntityRef] = Field(default_factory=list, max_length=MAX_TURN_REFERENCES)
    time_phrase: str | None = Field(default=None, max_length=MAX_TIME_PHRASE_LENGTH)
    # RFC 3339 UTC as-of instant for the turn. The HTTP transport ALWAYS stamps
    # this from the server clock, overriding any inbound value: a caller may not
    # assert the turn's freshness (a back-dated as-of would let a historical
    # window read as current, §12.3). It stays a field so the pure resolver never
    # reads a clock and in-process callers can inject a deterministic instant.
    now: str | None = None
    business_timezone: str = "UTC"
    week_starts_on: int = Field(default=0, ge=0, le=6)

    @field_validator("version", "recommendation_version", mode="before")
    @classmethod
    def _identifier_wire_form(cls, v: Any) -> Any:  # noqa: ANN401
        return _as_identifier(v, "version")

    @field_validator("business_timezone")
    @classmethod
    def _known_timezone(cls, v: str) -> str:
        # Calendar resolution is DATA; an unknown zone must fail closed at the
        # boundary rather than raise deep inside the pure resolver.
        try:
            ZoneInfo(v)
        except (ZoneInfoNotFoundError, ValueError) as exc:
            raise ValueError("business_timezone must be a known IANA timezone") from exc
        return v

    def to_chip(self) -> ContextChip:
        """Project the payload onto the single active chip the resolver validates.

        Provenance is carried through verbatim — including when it is ABSENT, so
        a provenance-less chip fails the resolver's scope check instead of being
        silently completed from the request.
        """
        return ContextChip(
            context_type=self.kind.to_context_type(),
            organization_id=self.organization_id,
            account_id=self.account_id,
            entity_id=self.entity_id,
            context_version=self.version,
            recommendation_version=self.recommendation_version,
        )

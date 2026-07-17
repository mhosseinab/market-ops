"""The deterministic context resolver (PRD §8.1, CHAT-007). Pure functions only.

No model, no I/O, no clock read: every input arrives on :class:`ResolveRequest`
(including ``now``), so resolution is a pure function and fully table-testable.
The rules, in order:

1. an explicit entity reference overrides a compatible active context;
2. an ambiguous reference (or a card-leading intent with no specific target)
   yields a PICKER — never a guessed card (CHAT-007);
3. otherwise the turn resolves against the single active context chip;
4. a time phrase always resolves to an explicit range + as-of (§8.1).
"""

from __future__ import annotations

import re
from datetime import UTC, datetime, timedelta

from llm.contextres.models import (
    CARD_CAPABLE_CONTEXTS,
    CARD_LEADING_INTENTS,
    ContextChip,
    EntityRef,
    PickerOption,
    Resolution,
    ResolutionKind,
    ResolveRequest,
    TimeRange,
)
from llm.intents.normalize import normalize_digits


def resolve(req: ResolveRequest) -> Resolution:
    """Resolve the single active context for a turn. Pure; never guesses.

    A card-leading intent that cannot pin exactly one specific target always
    returns a PICKER (CHAT-007); it never fabricates a subject or a card.
    """
    time_range = resolve_time_range(req.time_phrase, req.now) if req.time_phrase else None
    card_leading = req.intent in CARD_LEADING_INTENTS

    if req.references:
        return _resolve_with_references(req, time_range, card_leading)

    # No explicit reference: rely on the single active context chip.
    if req.active_context is None:
        # Nothing to act on. A card-leading turn needs a target ⇒ picker; any
        # other turn needs a context first ⇒ picker (never a guessed subject).
        return Resolution(
            kind=ResolutionKind.PICKER,
            options=[],
            time_range=time_range,
            reason="no_active_context",
        )

    if card_leading and req.active_context.context_type not in CARD_CAPABLE_CONTEXTS:
        # Account-level context can't be the subject of a card ⇒ pick a target.
        return Resolution(
            kind=ResolutionKind.PICKER,
            options=[],
            time_range=time_range,
            reason="account_level_context_needs_target",
        )

    # Resolve against the existing active chip (carry its bound identifiers).
    return Resolution(
        kind=ResolutionKind.RESOLVED,
        chip=req.active_context,
        time_range=time_range,
        reason="active_context",
    )


def _resolve_with_references(
    req: ResolveRequest, time_range: TimeRange | None, card_leading: bool
) -> Resolution:
    """Resolve when the message carried explicit entity references."""
    # Multiple distinct explicit references ⇒ ambiguous which is the subject.
    if len(req.references) > 1:
        return Resolution(
            kind=ResolutionKind.PICKER,
            options=_options_from_refs(req.references),
            time_range=time_range,
            reason="multiple_explicit_references",
        )

    ref = req.references[0]
    candidates = req.candidates.get(ref.raw, [])

    if len(candidates) == 1:
        # Unambiguous explicit reference overrides any compatible active context.
        return Resolution(
            kind=ResolutionKind.RESOLVED,
            chip=_chip_from_entity(candidates[0], req.account_id),
            time_range=time_range,
            reason="explicit_reference_override",
        )

    if len(candidates) == 0:
        # A reference that matches nothing. Fail closed — never invent a subject.
        return Resolution(
            kind=ResolutionKind.NOT_FOUND,
            time_range=time_range,
            reason="reference_matched_nothing",
        )

    # >1 candidate: ambiguous. A picker regardless of intent — a card-leading
    # turn must never guess a subject (CHAT-007), and a question must not either.
    return Resolution(
        kind=ResolutionKind.PICKER,
        options=_options_from_refs(candidates),
        time_range=time_range,
        reason="ambiguous_reference_card" if card_leading else "ambiguous_reference",
    )


def _chip_from_entity(entity: EntityRef, account_id: str | None) -> ContextChip:
    """Build the active chip a resolved entity activates."""
    return ContextChip(
        context_type=entity.context_type,
        account_id=account_id,
        entity_id=entity.entity_id,
    )


def _options_from_refs(refs: list[EntityRef]) -> list[PickerOption]:
    """Project candidate entities into structured, non-executable picker options."""
    return [
        PickerOption(
            context_type=r.context_type,
            entity_id=r.entity_id,
            label=r.label or r.raw,
        )
        for r in refs
    ]


# --- time-range resolution ---------------------------------------------------

# Phrase → (days_back, label catalog key). ``days_back`` is the inclusive window
# length; ``0`` means the current day. Persian and English share one table; the
# phrase is digit- and whitespace-normalized before lookup.
_FIXED_PHRASES: dict[str, tuple[int, str]] = {
    "today": (0, "time.range.today"),
    "امروز": (0, "time.range.today"),
    "yesterday": (1, "time.range.yesterday"),
    "دیروز": (1, "time.range.yesterday"),
    "this week": (6, "time.range.this_week"),
    "این هفته": (6, "time.range.this_week"),
    "last week": (7, "time.range.last_week"),
    "هفته گذشته": (7, "time.range.last_week"),
    "این ماه": (29, "time.range.this_month"),
    "this month": (29, "time.range.this_month"),
    "last month": (30, "time.range.last_month"),
    "ماه گذشته": (30, "time.range.last_month"),
}

# "last N days" / "N روز گذشته" / "N روز اخیر" — N captured after digit folding.
_LAST_N_DAYS = re.compile(
    r"(?:last\s+(\d+)\s+days?"
    r"|past\s+(\d+)\s+days?"
    r"|(\d+)\s*روز\s*(?:گذشته|اخیر|قبل))"
)


def resolve_time_range(phrase: str, now: str) -> TimeRange:
    """Resolve a time phrase to an explicit range + as-of (§8.1). Pure.

    ``now`` is the injected as-of clock (RFC 3339 UTC). Persian/Latin digits fold
    first (CHAT-081). An unrecognized phrase yields a single-day range anchored at
    ``now`` labelled ``time.range.unspecified`` — explicit, never an open range.
    """
    as_of = _parse_rfc3339(now)
    folded = normalize_digits(phrase).strip().lower()

    if folded in _FIXED_PHRASES:
        days_back, label_key = _FIXED_PHRASES[folded]
        return _range_ending_now(days_back, as_of, label_key)

    match = _LAST_N_DAYS.search(folded)
    if match is not None:
        n = int(next(g for g in match.groups() if g is not None))
        # N days inclusive of today ⇒ window length N-1 days back, min 0.
        return _range_ending_now(max(n - 1, 0), as_of, "time.range.last_n_days")

    # Unrecognized ⇒ explicit single-day range at as-of; never an open range.
    return _range_ending_now(0, as_of, "time.range.unspecified")


def _range_ending_now(days_back: int, as_of: datetime, label_key: str) -> TimeRange:
    start = (as_of - timedelta(days=days_back)).replace(
        hour=0, minute=0, second=0, microsecond=0
    )
    return TimeRange(
        start=_to_rfc3339(start),
        end=_to_rfc3339(as_of),
        as_of=_to_rfc3339(as_of),
        label_key=label_key,
    )


def _parse_rfc3339(value: str) -> datetime:
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=UTC)
    return parsed.astimezone(UTC)


def _to_rfc3339(value: datetime) -> str:
    return value.astimezone(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")

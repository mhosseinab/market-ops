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
from collections.abc import Sequence
from datetime import UTC, date, datetime, timedelta
from zoneinfo import ZoneInfo

from llm.contextres.models import (
    CARD_CAPABLE_CONTEXTS,
    CARD_LEADING_INTENTS,
    ContextChip,
    EntityCandidate,
    EntityRef,
    PickerOption,
    Resolution,
    ResolutionKind,
    ResolveRequest,
    TimeRange,
    missing_card_version_reason,
    scope_mismatch_reason,
)
from llm.intents.normalize import canonicalize_key, normalize_digits


def resolve(req: ResolveRequest) -> Resolution:
    """Resolve the single active context for a turn. Pure; never guesses.

    A card-leading intent that cannot pin exactly one specific target always
    returns a PICKER (CHAT-007); it never fabricates a subject or a card.
    """
    time_range = (
        resolve_time_range(
            req.time_phrase, req.now, req.business_timezone, req.week_starts_on
        )
        if req.time_phrase
        else None
    )
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

    # Tenant isolation first (PRD §12, §4.6): the active chip may only resolve if
    # its authoritative organization/account provenance is present and inside the
    # authenticated request scope. A stale or cross-tenant chip fails closed rather
    # than binding the wrong tenant — provenance is never manufactured.
    scope_reason = scope_mismatch_reason(
        req.active_context.organization_id,
        req.active_context.account_id,
        req.scope,
    )
    if scope_reason is not None:
        return Resolution(
            kind=ResolutionKind.NOT_FOUND,
            time_range=time_range,
            reason=scope_reason,
        )

    if card_leading and req.active_context.context_type not in CARD_CAPABLE_CONTEXTS:
        # Account-level context can't be the subject of a card ⇒ pick a target.
        return Resolution(
            kind=ResolutionKind.PICKER,
            options=[],
            time_range=time_range,
            reason="account_level_context_needs_target",
        )

    if card_leading:
        # The active chip will lead a card: it must carry the versions the card
        # binds and invalidates on (§8.1). A stale chip that dropped a required
        # version on re-fetch fails closed rather than binding an unversioned card.
        missing = missing_card_version_reason(
            req.active_context.context_type,
            req.active_context.context_version,
            req.active_context.recommendation_version,
        )
        if missing is not None:
            return Resolution(
                kind=ResolutionKind.NOT_FOUND,
                time_range=time_range,
                reason=missing,
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
    # Match on the BOUNDARY-canonical form (#29, CHAT-081/CHAT-080): the raw
    # reference token and every candidate-map key are folded through the same
    # normalizer, so a Persian/Arabic-Indic-digit or Arabic-Kaf/Yeh spelling
    # resolves identically to its canonical twin. Raw tokens are never mutated.
    candidates, colliding_keys = _match_candidates(req.candidates, ref.raw)

    # Tenant isolation before anything else (PRD §12, §4.6): validate EVERY
    # candidate's authoritative organization/account provenance against the
    # authenticated request scope. If any candidate is out of scope (or lacks
    # provenance), fail closed — a mixed set spanning two tenants must never
    # resolve directly, and a cross-tenant entry must never leak into a picker.
    # This runs over the FULL merged set (all canonically-colliding keys) so a
    # folded-key collision that spans tenants still fails closed.
    for candidate in candidates:
        scope_reason = scope_mismatch_reason(
            candidate.organization_id,
            candidate.account_id,
            req.scope,
        )
        if scope_reason is not None:
            return Resolution(
                kind=ResolutionKind.NOT_FOUND,
                time_range=time_range,
                reason=scope_reason,
            )

    # Canonicalization collision (#29): two or more DISTINCT raw candidate-map
    # keys fold to the reference's key. Which the user meant is genuinely
    # ambiguous, so picker over the UNION — never select arbitrarily, never
    # NOT_FOUND. Scope was already validated over the full merged set above.
    if colliding_keys > 1:
        return Resolution(
            kind=ResolutionKind.PICKER,
            options=_options_from_refs(candidates),
            time_range=time_range,
            reason="canonical_key_collision",
        )

    if len(candidates) == 1:
        candidate = candidates[0]
        if card_leading:
            # The resolved candidate will lead a card: it must carry the versions
            # the card binds and invalidates on (§8.1). Fail closed otherwise —
            # never emit a chip that a card can neither bind nor invalidate.
            missing = missing_card_version_reason(
                candidate.context_type,
                candidate.context_version,
                candidate.recommendation_version,
            )
            if missing is not None:
                return Resolution(
                    kind=ResolutionKind.NOT_FOUND,
                    time_range=time_range,
                    reason=missing,
                )
        # Unambiguous explicit reference overrides any compatible active context.
        return Resolution(
            kind=ResolutionKind.RESOLVED,
            chip=_chip_from_entity(candidate),
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


def _match_candidates(
    candidates: dict[str, list[EntityCandidate]], raw: str
) -> tuple[list[EntityCandidate], int]:
    """Match ``raw`` against candidate-map keys on their canonical form (#29).

    Both the reference token and every candidate key are folded through
    :func:`~llm.intents.normalize.canonicalize_key`, so accepted digit families
    and Arabic/Persian glyph variants of one identifier match. Returns the union
    of candidates from every non-empty key that canonicalizes equal, plus the
    count of DISTINCT such keys — ``> 1`` signals a canonicalization collision
    the caller must picker rather than resolve. Empty candidate lists never count
    as a match (a lone empty key stays ``reference_matched_nothing``). Insertion
    order is preserved, so the result is deterministic.
    """
    target = canonicalize_key(raw)
    merged: list[EntityCandidate] = []
    colliding_keys = 0
    for key, key_candidates in candidates.items():
        if not key_candidates:
            continue
        if canonicalize_key(key) == target:
            colliding_keys += 1
            merged.extend(key_candidates)
    return merged, colliding_keys


def _chip_from_entity(entity: EntityCandidate) -> ContextChip:
    """Build the active chip a resolved candidate activates.

    Organization/account provenance and both versions are carried from the
    authoritative candidate byte-for-byte (§8.1). Provenance is NEVER manufactured
    from the request scope: the candidate reached here only after passing
    :func:`scope_mismatch_reason`, so its own tenant identifiers are present and
    in-scope, and they are the only source of the chip's tenant.
    """
    return ContextChip(
        context_type=entity.context_type,
        organization_id=entity.organization_id,
        account_id=entity.account_id,
        entity_id=entity.entity_id,
        context_version=entity.context_version,
        recommendation_version=entity.recommendation_version,
    )


def _options_from_refs(refs: Sequence[EntityRef | EntityCandidate]) -> list[PickerOption]:
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

# The named calendar periods (#28). Each is resolved to its ACTUAL calendar
# boundaries under the configured business timezone — NOT a rolling "midnight N
# days ago through now" window. A COMPLETED period (yesterday/last week/last
# month) ends at the start of the next period and never includes the current day;
# a CURRENT period (today/this week/this month) runs from its calendar start up to
# ``as_of``. ``as_of`` stays a separate field in every case (§8.1).
_PERIOD_TODAY = "today"
_PERIOD_YESTERDAY = "yesterday"
_PERIOD_THIS_WEEK = "this_week"
_PERIOD_LAST_WEEK = "last_week"
_PERIOD_THIS_MONTH = "this_month"
_PERIOD_LAST_MONTH = "last_month"

# Phrase → (period kind, label catalog key). Persian and English share one table
# and the same resolution code path, so aliases resolve to IDENTICAL instants —
# the phrase language is never a branch (localization boundary, PRD §11). The
# phrase is digit- and whitespace-normalized before lookup.
_FIXED_PHRASES: dict[str, tuple[str, str]] = {
    "today": (_PERIOD_TODAY, "time.range.today"),
    "امروز": (_PERIOD_TODAY, "time.range.today"),
    "yesterday": (_PERIOD_YESTERDAY, "time.range.yesterday"),
    "دیروز": (_PERIOD_YESTERDAY, "time.range.yesterday"),
    "this week": (_PERIOD_THIS_WEEK, "time.range.this_week"),
    "این هفته": (_PERIOD_THIS_WEEK, "time.range.this_week"),
    "last week": (_PERIOD_LAST_WEEK, "time.range.last_week"),
    "هفته گذشته": (_PERIOD_LAST_WEEK, "time.range.last_week"),
    "این ماه": (_PERIOD_THIS_MONTH, "time.range.this_month"),
    "this month": (_PERIOD_THIS_MONTH, "time.range.this_month"),
    "last month": (_PERIOD_LAST_MONTH, "time.range.last_month"),
    "ماه گذشته": (_PERIOD_LAST_MONTH, "time.range.last_month"),
}

# Calendar-resolution DATA defaults (never a locale/phrase branch — PRD §11).
DEFAULT_BUSINESS_TIMEZONE = "UTC"
DEFAULT_WEEK_START = 0  # Monday (ISO 8601 weekday numbering, Mon=0 … Sun=6).

# Catalog keys for the "last N days" family (localization boundary — key strings
# only; the fa-IR/en copy lives in the locale pack, never authored here).
LABEL_RANGE_LAST_N_DAYS = "time.range.last_n_days"
LABEL_RANGE_UNSPECIFIED = "time.range.unspecified"
# A phrase that HAS the "last N days" shape but whose N is zero, out of the
# supported bound, or a pathologically long digit run: a DISTINCT typed outcome
# from ``unspecified`` (unrecognized phrase), so the response layer can treat it
# as a bounded clarification/failure instead of a plausible-looking default.
LABEL_RANGE_UNSUPPORTED = "time.range.unsupported"

# The inclusive upper bound on a user-supplied relative-day window. A year is well
# past the longest fixed phrase (last month = 30) and the deepest fixture
# ("last 90 days"), and days_back = MAX-1 = 364 is trivially inside timedelta's
# ~1e9-day limit, so int()/timedelta can never overflow once N passes this gate.
MAX_RELATIVE_DAYS = 365
# The most digits a supported N can have (``len("365") == 3``). Any longer digit
# run is out of range BY CONSTRUCTION, so it is rejected on length BEFORE int() —
# a run longer than Python 3.12's 4300-digit int↔str limit never reaches int().
_MAX_RELATIVE_DAYS_DIGITS = len(str(MAX_RELATIVE_DAYS))

# "last N days" / "N روز گذشته" / "N روز اخیر" — N captured after digit folding.
# The digit run is captured verbatim (still bounded downstream): a non-anchored
# search keeps embedded phrases matching, while numeric bounding lives in
# :func:`_resolve_relative_days` so a huge or overlong N fails closed, never
# crashes. The ``\d+`` is a simple, linear (non-nested) quantifier — no
# catastrophic backtracking — and int() is guarded by length before conversion.
_LAST_N_DAYS = re.compile(
    r"(?:last\s+(\d+)\s+days?"
    r"|past\s+(\d+)\s+days?"
    r"|(\d+)\s*روز\s*(?:گذشته|اخیر|قبل))"
)


def resolve_time_range(
    phrase: str,
    now: str,
    business_timezone: str = DEFAULT_BUSINESS_TIMEZONE,
    week_starts_on: int = DEFAULT_WEEK_START,
) -> TimeRange:
    """Resolve a time phrase to an explicit range + as-of (§8.1). Pure and TOTAL.

    ``now`` is the injected as-of clock (RFC 3339 UTC). ``business_timezone`` and
    ``week_starts_on`` are DATA (an IANA tz and an ISO weekday, Mon=0): a named
    calendar period's boundaries are computed on the business-tz calendar day/week/
    month, then stored/compared in UTC, while ``as_of`` stays the real ``now``. The
    phrase language is NEVER a branch — Persian and English aliases share this code
    path and resolve to IDENTICAL instants (localization boundary, PRD §11).

    Named calendar periods (``yesterday``, ``this/last week/month``) resolve to
    their ACTUAL calendar boundaries, DISTINCT from a rolling "last N days" window.
    Persian/Latin digits fold first (CHAT-081). An unrecognized phrase yields a
    single-day range anchored at ``now`` labelled ``time.range.unspecified`` —
    explicit, never an open range.

    A "last N days" phrase whose N is zero, above :data:`MAX_RELATIVE_DAYS`, or a
    pathologically long digit run fails closed as an explicit single-day range
    labelled :data:`LABEL_RANGE_UNSUPPORTED` (PRD §4.6, quarantine-over-inference):
    the bound is enforced BEFORE ``int()``/``timedelta``, so no user-controlled
    relative-day phrase can raise ``ValueError``/``OverflowError`` or a date-range
    error and 500 the turn.
    """
    as_of = _parse_rfc3339(now)
    tz = ZoneInfo(business_timezone)
    folded = normalize_digits(phrase).strip().lower()

    if folded in _FIXED_PHRASES:
        period, label_key = _FIXED_PHRASES[folded]
        return _named_calendar_range(period, as_of, tz, week_starts_on, label_key)

    match = _LAST_N_DAYS.search(folded)
    if match is not None:
        digits = next(g for g in match.groups() if g is not None)
        return _resolve_relative_days(digits, as_of, tz)

    # Unrecognized ⇒ explicit single-day range at as-of; never an open range.
    return _rolling_day_range(0, as_of, tz, LABEL_RANGE_UNSPECIFIED)


def _resolve_relative_days(digits: str, as_of: datetime, tz: ZoneInfo) -> TimeRange:
    """Bound a captured "last N days" digit run, then build its range. Pure.

    Fails closed to :data:`LABEL_RANGE_UNSUPPORTED` for anything outside
    ``1..MAX_RELATIVE_DAYS``. The length gate runs FIRST so an overlong run never
    reaches ``int()`` (Python 3.12 caps int↔str at 4300 digits); the numeric gate
    then keeps ``timedelta`` inside its limits. Only an in-range N builds a real
    window, so ``int()``/``timedelta`` are never reached with an unsafe value.

    This is the ROLLING family and stays DISTINCT from the named calendar periods:
    it always ends at ``as_of`` and floors N-1 business-tz days back from today.
    """
    if len(digits) > _MAX_RELATIVE_DAYS_DIGITS:
        return _rolling_day_range(0, as_of, tz, LABEL_RANGE_UNSUPPORTED)
    n = int(digits)
    if n < 1 or n > MAX_RELATIVE_DAYS:
        return _rolling_day_range(0, as_of, tz, LABEL_RANGE_UNSUPPORTED)
    # N days inclusive of today ⇒ window length N-1 days back (n>=1 ⇒ >=0).
    return _rolling_day_range(n - 1, as_of, tz, LABEL_RANGE_LAST_N_DAYS)


def _midnight_utc(day: date, tz: ZoneInfo) -> datetime:
    """UTC instant of local midnight of ``day`` in ``tz``.

    Constructing the aware datetime from the date components makes month/week
    arithmetic operate on calendar dates (DST-safe: each boundary is materialized
    at its OWN local wall-clock offset), then converts the result to UTC once."""
    return datetime(day.year, day.month, day.day, tzinfo=tz).astimezone(UTC)


def _first_of_month(day: date) -> date:
    return day.replace(day=1)


def _first_of_previous_month(day: date) -> date:
    if day.month == 1:
        return date(day.year - 1, 12, 1)
    return date(day.year, day.month - 1, 1)


def _week_start(day: date, week_starts_on: int) -> date:
    """First day of the calendar week containing ``day`` (week_starts_on: Mon=0)."""
    offset = (day.weekday() - week_starts_on) % 7
    return day - timedelta(days=offset)


def _named_calendar_range(
    period: str,
    as_of: datetime,
    tz: ZoneInfo,
    week_starts_on: int,
    label_key: str,
) -> TimeRange:
    """Resolve a named calendar period to explicit UTC boundaries (#28). Pure.

    Boundaries are the business-tz calendar day/week/month of ``as_of``. A CURRENT
    period ends at ``as_of``; a COMPLETED period ends at the start of the next
    period (so it never includes the current day). ``as_of`` is always carried
    separately. Variable month lengths, leap February, and year transitions fall
    out of :mod:`datetime` date arithmetic; DST is honored because each boundary is
    materialized at its own local offset in :func:`_midnight_utc`.
    """
    today = as_of.astimezone(tz).date()

    if period == _PERIOD_TODAY:
        start, end = _midnight_utc(today, tz), as_of
    elif period == _PERIOD_YESTERDAY:
        start = _midnight_utc(today - timedelta(days=1), tz)
        end = _midnight_utc(today, tz)  # start of today — today is excluded
    elif period == _PERIOD_THIS_WEEK:
        start, end = _midnight_utc(_week_start(today, week_starts_on), tz), as_of
    elif period == _PERIOD_LAST_WEEK:
        this_week = _week_start(today, week_starts_on)
        start = _midnight_utc(this_week - timedelta(days=7), tz)
        end = _midnight_utc(this_week, tz)  # start of this week — excluded
    elif period == _PERIOD_THIS_MONTH:
        start, end = _midnight_utc(_first_of_month(today), tz), as_of
    else:  # _PERIOD_LAST_MONTH
        this_month = _first_of_month(today)
        start = _midnight_utc(_first_of_previous_month(today), tz)
        end = _midnight_utc(this_month, tz)  # start of this month — excluded

    return TimeRange(
        start=_to_rfc3339(start),
        end=_to_rfc3339(end),
        as_of=_to_rfc3339(as_of),
        label_key=label_key,
    )


def _rolling_day_range(
    days_back: int, as_of: datetime, tz: ZoneInfo, label_key: str
) -> TimeRange:
    """A rolling window: ``days_back`` business-tz days back from today → as_of.

    Distinct from the named calendar periods — always ends at ``as_of`` and floors
    to the start of the business-tz day ``days_back`` days ago."""
    today = as_of.astimezone(tz).date()
    start = _midnight_utc(today - timedelta(days=days_back), tz)
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

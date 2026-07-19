"""Deterministic context-resolver tests (PRD §8.1, CHAT-007).

Table-driven and exhaustive over the resolution rules, plus a fixture-backed
containment check: EVERY ambiguous case in the eval corpus must produce a picker
and NONE may create a card (resolve to a specific-entity chip). The resolver is
pure — these tests never build a model.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from llm.contextres import (
    ContextChip,
    ContextType,
    EntityCandidate,
    EntityRef,
    RequestScope,
    Resolution,
    ResolutionKind,
    ResolveRequest,
    resolve,
    resolve_time_range,
)
from llm.contextres.resolver import (
    LABEL_RANGE_UNSUPPORTED,
    MAX_RELATIVE_DAYS,
)
from llm.intents.models import IntentClass

_FIXTURES = Path(__file__).resolve().parents[1] / "fixtures" / "evals" / "context"

# The authenticated tenant every single-tenant case runs under. Candidates and
# active-context chips carry this same provenance so they validate in-scope.
_ORG = "org-1"
_ACCOUNT = "acc-1"
_SCOPE = RequestScope(organization_id=_ORG, account_id=_ACCOUNT)


# --- rule table --------------------------------------------------------------


def _ref(ctype: ContextType, eid: str, raw: str) -> EntityRef:
    return EntityRef(context_type=ctype, entity_id=eid, raw=raw)


def _chip(ctype: ContextType, **kw: object) -> ContextChip:
    """An active-context chip with in-scope tenant provenance unless overridden."""
    fields: dict[str, object] = {"organization_id": _ORG, "account_id": _ACCOUNT}
    fields.update(kw)
    return ContextChip(context_type=ctype, **fields)  # type: ignore[arg-type]


def _cand(
    ctype: ContextType,
    eid: str,
    raw: str,
    *,
    organization_id: str | None = _ORG,
    account_id: str | None = _ACCOUNT,
    context_version: str | None = None,
    recommendation_version: str | None = None,
) -> EntityCandidate:
    """An authoritative candidate as a read tool would return it (with provenance).

    Organization/account default to the in-scope tenant; the cross-tenant negative
    tests pass a foreign organization/account (or ``None``) explicitly.
    """
    return EntityCandidate(
        context_type=ctype,
        entity_id=eid,
        raw=raw,
        organization_id=organization_id,
        account_id=account_id,
        context_version=context_version,
        recommendation_version=recommendation_version,
    )


def test_unambiguous_explicit_reference_resolves_and_overrides() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        active_context=_chip(ContextType.GLOBAL_ACCOUNT),
        references=[_ref(ContextType.PRODUCT, "", "SKU-9931")],
        candidates={
            "SKU-9931": [_cand(ContextType.PRODUCT, "p-9931", "SKU-9931", context_version="cv-7")]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_type is ContextType.PRODUCT
    assert res.chip.entity_id == "p-9931"  # overrode the account-level context
    assert res.reason == "explicit_reference_override"


def test_ambiguous_reference_pickers_and_creates_no_card() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "کفش")],
        candidates={
            "کفش": [
                _cand(ContextType.PRODUCT, "p1", "کفش"),
                _cand(ContextType.PRODUCT, "p2", "کفش"),
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert res.chip is None  # no card subject was invented
    assert len(res.options) == 2


def test_multiple_explicit_references_picker() -> None:
    req = ResolveRequest(
        intent=IntentClass.REVIEW_ACTION,
        scope=_SCOPE,
        references=[
            _ref(ContextType.PRODUCT, "p1", "SKU-1"),
            _ref(ContextType.PRODUCT, "p2", "SKU-2"),
        ],
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert len(res.options) == 2


def test_card_leading_with_account_context_pickers() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        active_context=_chip(ContextType.GLOBAL_ACCOUNT),
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert res.reason == "account_level_context_needs_target"


def test_card_leading_with_specific_context_resolves() -> None:
    chip = _chip(
        ContextType.PRODUCT,
        entity_id="e-9",
        context_version="cv-9",
    )
    req = ResolveRequest(intent=IntentClass.PREPARE_ACTION, scope=_SCOPE, active_context=chip)
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip == chip


def test_question_resolves_against_active_context() -> None:
    chip = _chip(ContextType.PRODUCT, entity_id="e-3")
    req = ResolveRequest(intent=IntentClass.QUESTION, scope=_SCOPE, active_context=chip)
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED


def test_no_context_and_no_reference_pickers() -> None:
    req = ResolveRequest(intent=IntentClass.QUESTION, scope=_SCOPE)
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert res.reason == "no_active_context"


def test_reference_matching_nothing_is_not_found() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-0000")],
        candidates={"SKU-0000": []},
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND


# --- version binding / stale-card containment (#30, PRD §8.1) ----------------
# A card binds resolved entity + account + context version + recommendation
# version. Versions must survive resolution byte-for-byte; a card-leading intent
# that would resolve a subject missing a required version fails closed (never a
# chip that cannot be bound or invalidated).


def test_explicit_reference_preserves_context_version_for_product() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={"SKU-1": [_cand(ContextType.PRODUCT, "p-1", "SKU-1", context_version="cv-42")]},
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_version == "cv-42"  # byte-for-byte


def test_explicit_reference_preserves_both_versions_for_recommendation() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.RECOMMENDATION, "", "REC-1")],
        candidates={
            "REC-1": [
                _cand(
                    ContextType.RECOMMENDATION,
                    "r-1",
                    "REC-1",
                    context_version="cv-9",
                    recommendation_version="rv-3",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_version == "cv-9"
    assert res.chip.recommendation_version == "rv-3"


def test_resolved_chip_carries_candidate_tenant_provenance() -> None:
    """The emitted chip's organization/account come only from the authoritative,
    in-scope candidate — never manufactured from the request scope."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    organization_id=_ORG,
                    account_id=_ACCOUNT,
                    context_version="cv-1",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.chip is not None
    assert res.chip.organization_id == _ORG
    assert res.chip.account_id == _ACCOUNT


def test_card_leading_explicit_reference_missing_context_version_fails_closed() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={"SKU-1": [_cand(ContextType.PRODUCT, "p-1", "SKU-1")]},  # no version
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND  # never a chip that can't bind a card
    assert res.chip is None
    assert res.reason == "missing_context_version"


def test_card_leading_recommendation_missing_recommendation_version_fails_closed() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.RECOMMENDATION, "", "REC-1")],
        candidates={
            "REC-1": [
                _cand(ContextType.RECOMMENDATION, "r-1", "REC-1", context_version="cv-9")
            ]  # context_version present, recommendation_version absent
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_recommendation_version"


def test_non_card_leading_reference_without_version_still_resolves() -> None:
    """Navigation creates no card, so an absent version is not fail-closed."""
    req = ResolveRequest(
        intent=IntentClass.NAVIGATION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={"SKU-1": [_cand(ContextType.PRODUCT, "p-1", "SKU-1")]},
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_version is None


def test_active_context_card_leading_preserves_versions() -> None:
    chip = _chip(
        ContextType.RECOMMENDATION,
        entity_id="r-1",
        context_version="cv-2",
        recommendation_version="rv-5",
    )
    res = resolve(
        ResolveRequest(intent=IntentClass.REVIEW_ACTION, scope=_SCOPE, active_context=chip)
    )
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_version == "cv-2"
    assert res.chip.recommendation_version == "rv-5"


def test_active_context_card_leading_missing_context_version_fails_closed() -> None:
    """A stale active chip (version dropped on re-fetch) must not lead a card."""
    chip = _chip(ContextType.PRODUCT, entity_id="e-9")
    res = resolve(
        ResolveRequest(intent=IntentClass.PREPARE_ACTION, scope=_SCOPE, active_context=chip)
    )
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_context_version"


def test_active_context_recommendation_missing_recommendation_version_fails_closed() -> None:
    chip = _chip(
        ContextType.RECOMMENDATION,
        entity_id="r-1",
        context_version="cv-2",
    )
    res = resolve(
        ResolveRequest(intent=IntentClass.PREPARE_ACTION, scope=_SCOPE, active_context=chip)
    )
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_recommendation_version"


def test_active_context_question_without_version_still_resolves() -> None:
    """A read-only Question resolves against a versionless chip (no card bound)."""
    chip = _chip(ContextType.PRODUCT, entity_id="e-9")
    res = resolve(ResolveRequest(intent=IntentClass.QUESTION, scope=_SCOPE, active_context=chip))
    assert res.kind is ResolutionKind.RESOLVED


def test_exactly_one_active_context_on_resolve() -> None:
    """A RESOLVED outcome yields exactly one chip; a PICKER yields none."""
    resolved = resolve(
        ResolveRequest(
            intent=IntentClass.QUESTION,
            scope=_SCOPE,
            active_context=_chip(ContextType.PRODUCT, entity_id="e"),
        )
    )
    assert resolved.chip is not None and resolved.options == []
    picker = resolve(ResolveRequest(intent=IntentClass.QUESTION, scope=_SCOPE))
    assert picker.chip is None


# --- organization / account provenance containment (#32, PRD §12, §4.6) ------
# Tenant isolation: a candidate or active context assembled from organization/
# account A must NEVER resolve under a request authenticated for B. Every
# provenance field is validated against the request scope BEFORE resolution;
# a mismatch (or missing provenance) fails closed with a stable reason token,
# and provenance is never manufactured from the request scope.

_OTHER_ORG = "org-2"
_OTHER_ACCOUNT = "acc-2"


def test_explicit_reference_cross_account_candidate_fails_closed() -> None:
    """A candidate owned by another account never resolves under this scope."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,  # org-1 / acc-1
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    account_id=_OTHER_ACCOUNT,  # foreign account, valid version
                    context_version="cv-1",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None  # never a relabeled chip in the caller's tenant
    assert res.reason == "account_scope_mismatch"


def test_explicit_reference_cross_organization_candidate_fails_closed() -> None:
    """A candidate from another organization never resolves under this scope."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    organization_id=_OTHER_ORG,  # foreign organization
                    context_version="cv-1",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "organization_scope_mismatch"


def test_explicit_reference_candidate_without_account_is_not_manufactured() -> None:
    """A candidate lacking account provenance fails closed — it must NOT inherit
    the request account (the old ``entity.account_id or account_id`` bug)."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    account_id=None,  # missing provenance
                    context_version="cv-1",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None  # not a chip stamped with acc-1
    assert res.reason == "missing_account_provenance"


def test_explicit_reference_candidate_without_organization_fails_closed() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    organization_id=None,
                    context_version="cv-1",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_organization_provenance"


def test_mixed_candidate_set_across_two_accounts_never_resolves() -> None:
    """A candidate list spanning two accounts fails closed — it never silently
    filters to the in-scope entry, nor leaks the foreign one into a picker."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(ContextType.PRODUCT, "in-scope", "SKU-1", context_version="cv-a"),
                _cand(
                    ContextType.PRODUCT,
                    "foreign",
                    "SKU-1",
                    account_id=_OTHER_ACCOUNT,
                    context_version="cv-b",
                ),
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.options == []  # no picker option built from a cross-tenant set
    assert res.reason == "account_scope_mismatch"


def test_mixed_candidate_set_across_two_organizations_never_resolves() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(ContextType.PRODUCT, "in-scope", "SKU-1", context_version="cv-a"),
                _cand(
                    ContextType.PRODUCT,
                    "foreign",
                    "SKU-1",
                    organization_id=_OTHER_ORG,
                    context_version="cv-b",
                ),
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.options == []
    assert res.reason == "organization_scope_mismatch"


def test_active_context_cross_account_fails_closed() -> None:
    """A restored active chip from another account must not resolve or bind."""
    chip = _chip(
        ContextType.PRODUCT,
        account_id=_OTHER_ACCOUNT,
        entity_id="e-9",
        context_version="cv-9",
    )
    res = resolve(ResolveRequest(intent=IntentClass.QUESTION, scope=_SCOPE, active_context=chip))
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "account_scope_mismatch"


def test_active_context_cross_organization_fails_closed() -> None:
    chip = _chip(
        ContextType.PRODUCT,
        organization_id=_OTHER_ORG,
        entity_id="e-9",
        context_version="cv-9",
    )
    res = resolve(
        ResolveRequest(intent=IntentClass.PREPARE_ACTION, scope=_SCOPE, active_context=chip)
    )
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "organization_scope_mismatch"


def test_active_context_missing_organization_provenance_fails_closed() -> None:
    """A chip lacking organization provenance fails closed rather than resolving
    on account alone — scope is validated before card-version binding."""
    chip = ContextChip(
        context_type=ContextType.PRODUCT,
        account_id=_ACCOUNT,  # no organization_id
        entity_id="e-9",
        context_version="cv-9",
    )
    res = resolve(ResolveRequest(intent=IntentClass.QUESTION, scope=_SCOPE, active_context=chip))
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_organization_provenance"


def test_cross_account_scope_check_precedes_card_version_check() -> None:
    """Tenant isolation is evaluated before the card-version gate: a foreign
    candidate that also lacks a version reports the scope mismatch, not the
    version — the scope guard is unconditional and first."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    account_id=_OTHER_ACCOUNT,  # foreign AND no version
                )
            ]
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.reason == "account_scope_mismatch"


# --- time-range resolution ---------------------------------------------------


# now = Friday 2026-07-17 09:30Z. A named CALENDAR period carries the correctly
# resolved boundary (#28): a COMPLETED period (yesterday) ends at the start of the
# next period, NOT at ``now``; a CURRENT period (today) still ends at ``now``. A
# rolling "last N days" window is DISTINCT and always ends at ``now``.
@pytest.mark.parametrize(
    ("phrase", "expected_start", "expected_end", "expected_label"),
    [
        ("today", "2026-07-17T00:00:00Z", "2026-07-17T09:30:00Z", "time.range.today"),
        ("امروز", "2026-07-17T00:00:00Z", "2026-07-17T09:30:00Z", "time.range.today"),
        # Yesterday is a COMPLETED day: it ends at the start of today, not at now.
        ("yesterday", "2026-07-16T00:00:00Z", "2026-07-17T00:00:00Z", "time.range.yesterday"),
        ("دیروز", "2026-07-16T00:00:00Z", "2026-07-17T00:00:00Z", "time.range.yesterday"),
        ("last 7 days", "2026-07-11T00:00:00Z", "2026-07-17T09:30:00Z", "time.range.last_n_days"),
        ("۷ روز گذشته", "2026-07-11T00:00:00Z", "2026-07-17T09:30:00Z", "time.range.last_n_days"),
        ("past 5 days", "2026-07-13T00:00:00Z", "2026-07-17T09:30:00Z", "time.range.last_n_days"),
        ("something odd", "2026-07-17T00:00:00Z", "2026-07-17T09:30:00Z", "time.range.unspecified"),
    ],
)
def test_time_range_is_explicit_with_as_of(
    phrase: str, expected_start: str, expected_end: str, expected_label: str
) -> None:
    now = "2026-07-17T09:30:00Z"
    tr = resolve_time_range(phrase, now)
    # Always an explicit closed range plus an as-of instant (§8.1).
    assert tr.start == expected_start
    assert tr.end == expected_end
    assert tr.as_of == now  # as-of stays the real now, separate from the range end.
    assert tr.label_key == expected_label


def test_resolve_attaches_time_range_when_phrase_present() -> None:
    req = ResolveRequest(
        intent=IntentClass.QUESTION,
        scope=_SCOPE,
        active_context=_chip(ContextType.PRODUCT, entity_id="e"),
        time_phrase="۷ روز گذشته",
        now="2026-07-17T09:30:00Z",
    )
    res = resolve(req)
    assert res.time_range is not None
    assert res.time_range.as_of == "2026-07-17T09:30:00Z"


# --- named calendar periods (#28, PRD §8.1) ----------------------------------
# A named calendar period ("yesterday", "this/last week", "this/last month") is
# resolved to its ACTUAL calendar boundaries under the configured business
# timezone — never a rolling "midnight N days ago through now" window. as_of stays
# separate. Persian and English aliases MUST resolve to IDENTICAL instants because
# the phrase language is not a branch: boundaries come from the (tz, week-start)
# DATA, not from which language named the period.

# now = Friday 2026-07-17 09:30Z; default business tz = UTC, week starts Monday.
_NOW_FRI = "2026-07-17T09:30:00Z"


def test_yesterday_excludes_today_ends_at_start_of_today() -> None:
    tr = resolve_time_range("yesterday", _NOW_FRI)
    assert tr.start == "2026-07-16T00:00:00Z"  # start of yesterday
    assert tr.end == "2026-07-17T00:00:00Z"  # START OF TODAY — today is excluded
    assert tr.as_of == _NOW_FRI  # as-of is the real now, distinct from the range end
    assert tr.end < tr.as_of  # the current day is NOT inside a "yesterday" range
    assert tr.label_key == "time.range.yesterday"


def test_this_week_uses_calendar_week_start_not_rolling_seven_days() -> None:
    # Monday-start week containing Friday 2026-07-17 begins Monday 2026-07-13.
    tr = resolve_time_range("this week", _NOW_FRI)
    assert tr.start == "2026-07-13T00:00:00Z"  # calendar week start (Monday)
    assert tr.end == _NOW_FRI  # current period runs up to now
    assert tr.label_key == "time.range.this_week"


def test_last_week_is_the_prior_calendar_week_not_a_rolling_span() -> None:
    # Prior full Monday..Sunday week: 2026-07-06 .. up to (not incl) this week.
    tr = resolve_time_range("last week", _NOW_FRI)
    assert tr.start == "2026-07-06T00:00:00Z"  # start of previous calendar week
    assert tr.end == "2026-07-13T00:00:00Z"  # start of THIS week — excludes this week
    assert tr.as_of == _NOW_FRI
    assert tr.label_key == "time.range.last_week"


def test_this_month_uses_month_start_not_rolling_thirty_days() -> None:
    tr = resolve_time_range("this month", _NOW_FRI)
    assert tr.start == "2026-07-01T00:00:00Z"  # first of the month
    assert tr.end == _NOW_FRI  # current month up to now
    assert tr.label_key == "time.range.this_month"


def test_last_month_uses_actual_month_boundaries() -> None:
    tr = resolve_time_range("last month", _NOW_FRI)
    assert tr.start == "2026-06-01T00:00:00Z"  # first of previous month
    assert tr.end == "2026-07-01T00:00:00Z"  # first of this month — excludes this month
    assert tr.label_key == "time.range.last_month"


def test_last_month_spans_variable_length_leap_february() -> None:
    # now in March 2024 (a leap year): "last month" is all of February (29 days).
    tr = resolve_time_range("last month", "2024-03-15T10:00:00Z")
    assert tr.start == "2024-02-01T00:00:00Z"
    assert tr.end == "2024-03-01T00:00:00Z"  # 29-day span, ends at March 1
    assert tr.label_key == "time.range.last_month"


def test_yesterday_resolves_leap_day() -> None:
    # On March 1 of a leap year, "yesterday" is Feb 29 — a date that exists only
    # because month lengths and leap years are honored, not a 28-day assumption.
    tr = resolve_time_range("yesterday", "2024-03-01T08:00:00Z")
    assert tr.start == "2024-02-29T00:00:00Z"
    assert tr.end == "2024-03-01T00:00:00Z"


def test_last_month_across_year_transition() -> None:
    # now in January: "last month" is the previous December of the PRIOR year.
    tr = resolve_time_range("last month", "2026-01-10T05:00:00Z")
    assert tr.start == "2025-12-01T00:00:00Z"
    assert tr.end == "2026-01-01T00:00:00Z"


def test_last_week_across_year_transition() -> None:
    # now = Thursday 2026-01-01. This Monday-start week began Mon 2025-12-29, so
    # last week is 2025-12-22 .. 2025-12-29 — spanning the year boundary.
    tr = resolve_time_range("last week", "2026-01-01T12:00:00Z")
    assert tr.start == "2025-12-22T00:00:00Z"
    assert tr.end == "2025-12-29T00:00:00Z"


def test_named_period_honors_business_timezone_offset() -> None:
    # Asia/Tehran is UTC+03:30 (no DST since 2022). At 2026-07-16T21:00Z it is
    # already 2026-07-17T00:30 in Tehran, so "yesterday" (Tehran) is the Tehran
    # calendar day 2026-07-16 — a DIFFERENT instant span than UTC yesterday.
    now = "2026-07-16T21:00:00Z"
    tr = resolve_time_range("yesterday", now, "Asia/Tehran")
    assert tr.start == "2026-07-15T20:30:00Z"  # Tehran 2026-07-16 00:00 in UTC
    assert tr.end == "2026-07-16T20:30:00Z"  # Tehran 2026-07-17 00:00 in UTC
    assert tr.end < tr.as_of  # excludes the current Tehran day
    # UTC resolution of the same instant differs — the tz offset is load-bearing.
    utc = resolve_time_range("yesterday", now, "UTC")
    assert utc.start != tr.start


def test_this_month_boundary_computed_at_its_own_dst_offset() -> None:
    # America/New_York: March 1 is EST (-05:00); now (Mar 15) is EDT (-04:00) after
    # the Mar 10 spring-forward. The month start must use March-1's OWN offset, so
    # 2024-03-01 00:00 local = 05:00Z, NOT 04:00Z.
    tr = resolve_time_range("this month", "2024-03-15T12:00:00Z", "America/New_York")
    assert tr.start == "2024-03-01T05:00:00Z"  # EST offset at the boundary date
    assert tr.end == "2024-03-15T12:00:00Z"


def test_last_week_range_crossing_dst_transition() -> None:
    # America/New_York, now = Wednesday 2024-03-13 (EDT). This week starts Mon
    # 2024-03-11 (EDT, -04:00 → 04:00Z). Last week starts Mon 2024-03-04 (EST,
    # -05:00 → 05:00Z) and ends at this week's start. The two boundaries carry
    # DIFFERENT offsets across the Mar 10 spring-forward — each at its own wall time.
    tr = resolve_time_range("last week", "2024-03-13T12:00:00Z", "America/New_York")
    assert tr.start == "2024-03-04T05:00:00Z"  # EST boundary
    assert tr.end == "2024-03-11T04:00:00Z"  # EDT boundary
    assert tr.label_key == "time.range.last_week"


@pytest.mark.parametrize(
    ("en", "fa"),
    [
        ("yesterday", "دیروز"),
        ("this week", "این هفته"),
        ("last week", "هفته گذشته"),
        ("this month", "این ماه"),
        ("last month", "ماه گذشته"),
        ("today", "امروز"),
    ],
)
@pytest.mark.parametrize("tz", ["UTC", "Asia/Tehran", "America/New_York"])
def test_persian_and_english_named_periods_resolve_to_identical_instants(
    en: str, fa: str, tz: str
) -> None:
    now = "2026-07-17T09:30:00Z"
    en_tr = resolve_time_range(en, now, tz)
    fa_tr = resolve_time_range(fa, now, tz)
    # Phrase language is NOT a branch: identical resolved instants and label.
    assert en_tr.start == fa_tr.start
    assert en_tr.end == fa_tr.end
    assert en_tr.as_of == fa_tr.as_of
    assert en_tr.label_key == fa_tr.label_key


def test_configurable_week_start_shifts_calendar_week() -> None:
    # A Saturday-start week (week_starts_on=5) is DATA, not a locale branch: it
    # shifts the boundary deterministically regardless of phrase language.
    # Friday 2026-07-17 with a Saturday-start week began Sat 2026-07-11.
    tr = resolve_time_range("this week", _NOW_FRI, "UTC", 5)
    assert tr.start == "2026-07-11T00:00:00Z"
    fa = resolve_time_range("این هفته", _NOW_FRI, "UTC", 5)
    assert fa.start == tr.start  # language-independent


def test_resolve_threads_business_timezone_into_named_period() -> None:
    # The top-level resolver passes the request's business timezone (DATA) into
    # calendar-period resolution, so "yesterday" is the business-tz calendar day.
    req = ResolveRequest(
        intent=IntentClass.QUESTION,
        scope=_SCOPE,
        active_context=_chip(ContextType.PRODUCT, entity_id="e"),
        time_phrase="yesterday",
        now="2026-07-16T21:00:00Z",
        business_timezone="Asia/Tehran",
    )
    res = resolve(req)
    assert res.time_range is not None
    assert res.time_range.start == "2026-07-15T20:30:00Z"
    assert res.time_range.end == "2026-07-16T20:30:00Z"


def test_rolling_last_n_days_stays_distinct_from_named_periods() -> None:
    # "last 7 days" is a ROLLING window ending at now — NOT the "last week" named
    # calendar period. Their ranges and labels must differ (#28).
    rolling = resolve_time_range("last 7 days", _NOW_FRI)
    named = resolve_time_range("last week", _NOW_FRI)
    assert rolling.label_key == "time.range.last_n_days"
    assert named.label_key == "time.range.last_week"
    assert rolling.end == _NOW_FRI  # rolling ends at now
    assert named.end == "2026-07-13T00:00:00Z"  # named ends at this week's start
    assert rolling.start != named.start


# --- bounded relative-day resolution (#34, PRD §4.6 fail-closed) --------------
# "last N days" / "N روز گذشته" carries a user-controlled integer. It must be
# BOUNDED and VALIDATED before int()/timedelta so a huge, zero, or pathologically
# long digit run fails closed as an explicit typed range — never an uncaught
# ValueError/OverflowError that 500s the chat turn. resolve_time_range stays a
# TOTAL pure function (§8.1: always an explicit range + as-of, never open).

_NOW = "2026-07-17T09:30:00Z"
_KNOWN_TIME_LABELS = {
    "time.range.today",
    "time.range.yesterday",
    "time.range.this_week",
    "time.range.last_week",
    "time.range.this_month",
    "time.range.last_month",
    "time.range.last_n_days",
    "time.range.unspecified",
    LABEL_RANGE_UNSUPPORTED,
}

# Every family normalize_digits folds, plus Latin — so the property tests exercise
# arbitrary Unicode digit families after boundary folding (CHAT-081).
_LATIN = "0123456789"
_PERSIAN = "۰۱۲۳۴۵۶۷۸۹"
_ARABIC = "٠١٢٣٤٥٦٧٨٩"


@pytest.mark.parametrize(
    "phrase",
    [
        "last 10000000000000 days",  # timedelta OverflowError territory
        "last 999999999999999999999999 days",  # far past int64
        "past 366 days",  # just over the supported bound
        "۱۰۰۰۰۰۰۰۰۰۰۰۰۰ روز گذشته",  # huge Persian-digit N
        "last 0 days",  # degenerate zero → clarification, not "today"
        "۰ روز گذشته",  # Persian zero
        "last " + "9" * 5000 + " days",  # > int↔str 4300-digit limit (ValueError)
        "۹" * 6000 + " روز اخیر",  # overlong Persian-digit run
    ],
)
def test_out_of_range_relative_days_fail_closed_unsupported(phrase: str) -> None:
    # No exception may escape (ValueError/OverflowError/date-range) — fail closed.
    tr = resolve_time_range(phrase, _NOW)
    assert tr.label_key == LABEL_RANGE_UNSUPPORTED
    # Explicit, never an open range; anchored at as-of, never an overflowed date.
    assert tr.as_of == _NOW
    assert tr.end == _NOW
    assert tr.start == "2026-07-17T00:00:00Z"


def test_unsupported_is_distinct_from_unspecified() -> None:
    """An out-of-range 'last N days' shape is a DISTINCT typed outcome from an
    unrecognized phrase — the response layer treats them differently."""
    unsupported = resolve_time_range("last 5000 days", _NOW)
    unspecified = resolve_time_range("something odd", _NOW)
    assert unsupported.label_key == LABEL_RANGE_UNSUPPORTED
    assert unspecified.label_key == "time.range.unspecified"
    assert unsupported.label_key != unspecified.label_key


def test_valid_minimum_relative_day_resolves_deterministically() -> None:
    tr = resolve_time_range("last 1 days", _NOW)
    assert tr.label_key == "time.range.last_n_days"
    assert tr.start == "2026-07-17T00:00:00Z"  # 1 day inclusive ⇒ today
    assert tr.end == _NOW
    assert tr.as_of == _NOW


def test_valid_maximum_relative_day_resolves_deterministically() -> None:
    tr = resolve_time_range(f"last {MAX_RELATIVE_DAYS} days", _NOW)
    assert tr.label_key == "time.range.last_n_days"
    # MAX inclusive ⇒ window length MAX-1 days back, well within timedelta limits.
    expected = "2025-07-18T00:00:00Z"  # 2026-07-17 minus 364 days
    assert tr.start == expected


def test_one_over_maximum_is_unsupported_but_at_boundary_is_valid() -> None:
    assert resolve_time_range(f"last {MAX_RELATIVE_DAYS} days", _NOW).label_key == (
        "time.range.last_n_days"
    )
    assert resolve_time_range(f"last {MAX_RELATIVE_DAYS + 1} days", _NOW).label_key == (
        LABEL_RANGE_UNSUPPORTED
    )


@pytest.mark.parametrize("family", [_LATIN, _PERSIAN, _ARABIC])
@pytest.mark.parametrize("length", [1, 2, 3, 4, 8, 40, 400, 4301, 6000])
def test_arbitrary_digit_family_and_length_never_raises(family: str, length: int) -> None:
    """Property: any digit family (post-fold) at any length resolves TOTALLY to a
    known typed label and never raises. Covers the int↔str >4300 limit and the
    timedelta overflow band across Latin/Persian/Arabic digits."""
    digit = family[9]  # '9' in each family
    for template in (f"last {digit * length} days", f"{digit * length} روز گذشته"):
        tr = resolve_time_range(template, _NOW)
        assert tr.label_key in _KNOWN_TIME_LABELS
        assert tr.as_of == _NOW  # never an overflowed / open range


def test_resolve_top_level_never_crashes_on_pathological_time_phrase() -> None:
    """The full resolver path fails closed too: a huge time phrase yields an
    unsupported range, not a 500."""
    req = ResolveRequest(
        intent=IntentClass.QUESTION,
        scope=_SCOPE,
        active_context=_chip(ContextType.PRODUCT, entity_id="e"),
        time_phrase="last " + "9" * 5000 + " days",
        now=_NOW,
    )
    res = resolve(req)
    assert res.time_range is not None
    assert res.time_range.label_key == LABEL_RANGE_UNSUPPORTED


# --- fixture-backed containment (CHAT-007) -----------------------------------


def _load(path: Path) -> list[dict[str, Any]]:
    with path.open(encoding="utf-8") as fh:
        return [json.loads(line) for line in fh if line.strip()]


def _request_from_case(case: dict[str, Any]) -> ResolveRequest:
    return ResolveRequest.model_validate(
        {
            "intent": case["intent"],
            "scope": case["scope"],
            "active_context": case["active_context"],
            "references": case["references"],
            "candidates": case["candidates"],
            "time_phrase": case["time_phrase"],
            "now": case["now"],
        }
    )


def test_every_ambiguous_fixture_pickers_and_creates_no_card() -> None:
    """100% of ambiguous action fixtures ⇒ picker; ZERO create a card (CHAT-007)."""
    cases = _load(_FIXTURES / "context_ambiguous.jsonl")
    assert cases, "ambiguous fixtures must exist"
    for case in cases:
        assert case["ambiguous"] is True, case["id"]
        res: Resolution = resolve(_request_from_case(case))
        assert res.kind is ResolutionKind.PICKER, f"{case['id']} did not picker"
        # The card-creation guard: no specific-entity chip was produced.
        assert res.chip is None, f"{case['id']} created a card subject"


def test_every_context_fixture_matches_expected_kind() -> None:
    """Both fixture files resolve to their authored expected kind (S24 corpus)."""
    for name in ("context_ambiguous.jsonl", "context_resolved.jsonl"):
        for case in _load(_FIXTURES / name):
            res = resolve(_request_from_case(case))
            assert res.kind.value == case["expected"]["kind"], case["id"]
            if res.kind is ResolutionKind.RESOLVED:
                assert res.chip is not None
                expected_ctype = case["expected"]["context_type"]
                if expected_ctype is not None:
                    assert res.chip.context_type.value == expected_ctype, case["id"]


def test_card_leading_resolved_fixtures_carry_required_versions() -> None:
    """Every resolved card-leading fixture binds the versions a card needs (§8.1).

    A card-capable resolution under a card-leading intent must carry
    ``context_version`` (and ``recommendation_version`` for Recommendation) — the
    exact fields a PrepareAction card binds and invalidates on. This is the gap
    #30 closed: versions must survive resolution, not be silently dropped.
    """
    from llm.contextres.models import CARD_CAPABLE_CONTEXTS, CARD_LEADING_INTENTS

    checked = 0
    for case in _load(_FIXTURES / "context_resolved.jsonl"):
        if IntentClass(case["intent"]) not in CARD_LEADING_INTENTS:
            continue
        if case["expected"]["kind"] != ResolutionKind.RESOLVED.value:
            continue
        res = resolve(_request_from_case(case))
        assert res.kind is ResolutionKind.RESOLVED, case["id"]
        assert res.chip is not None
        if res.chip.context_type in CARD_CAPABLE_CONTEXTS:
            assert res.chip.context_version, case["id"]
            if res.chip.context_type is ContextType.RECOMMENDATION:
                assert res.chip.recommendation_version, case["id"]
            checked += 1
    assert checked > 0, "expected card-leading resolved fixtures to exist"

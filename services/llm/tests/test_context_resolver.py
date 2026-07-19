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
    Resolution,
    ResolutionKind,
    ResolveRequest,
    resolve,
    resolve_time_range,
)
from llm.intents.models import IntentClass

_FIXTURES = Path(__file__).resolve().parents[1] / "fixtures" / "evals" / "context"


# --- rule table --------------------------------------------------------------


def _ref(ctype: ContextType, eid: str, raw: str) -> EntityRef:
    return EntityRef(context_type=ctype, entity_id=eid, raw=raw)


def _cand(
    ctype: ContextType,
    eid: str,
    raw: str,
    *,
    account_id: str | None = None,
    context_version: str | None = None,
    recommendation_version: str | None = None,
) -> EntityCandidate:
    """An authoritative candidate as a read tool would return it (with provenance)."""
    return EntityCandidate(
        context_type=ctype,
        entity_id=eid,
        raw=raw,
        account_id=account_id,
        context_version=context_version,
        recommendation_version=recommendation_version,
    )


def test_unambiguous_explicit_reference_resolves_and_overrides() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        account_id="acc-1",
        active_context=ContextChip(context_type=ContextType.GLOBAL_ACCOUNT, account_id="acc-1"),
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
        active_context=ContextChip(context_type=ContextType.GLOBAL_ACCOUNT, account_id="acc-1"),
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert res.reason == "account_level_context_needs_target"


def test_card_leading_with_specific_context_resolves() -> None:
    chip = ContextChip(
        context_type=ContextType.PRODUCT,
        account_id="acc-1",
        entity_id="e-9",
        context_version="cv-9",
    )
    req = ResolveRequest(intent=IntentClass.PREPARE_ACTION, active_context=chip)
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip == chip


def test_question_resolves_against_active_context() -> None:
    chip = ContextChip(context_type=ContextType.PRODUCT, entity_id="e-3")
    req = ResolveRequest(intent=IntentClass.QUESTION, active_context=chip)
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED


def test_no_context_and_no_reference_pickers() -> None:
    req = ResolveRequest(intent=IntentClass.QUESTION)
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert res.reason == "no_active_context"


def test_reference_matching_nothing_is_not_found() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
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
        account_id="acc-1",
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
        account_id="acc-1",
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


def test_candidate_account_provenance_overrides_request_account() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        account_id="acc-req",
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={
            "SKU-1": [
                _cand(
                    ContextType.PRODUCT,
                    "p-1",
                    "SKU-1",
                    account_id="acc-prov",
                    context_version="cv-1",
                )
            ]
        },
    )
    res = resolve(req)
    assert res.chip is not None
    assert res.chip.account_id == "acc-prov"


def test_card_leading_explicit_reference_missing_context_version_fails_closed() -> None:
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        account_id="acc-1",
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
        account_id="acc-1",
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
        account_id="acc-1",
        references=[_ref(ContextType.PRODUCT, "", "SKU-1")],
        candidates={"SKU-1": [_cand(ContextType.PRODUCT, "p-1", "SKU-1")]},
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_version is None


def test_active_context_card_leading_preserves_versions() -> None:
    chip = ContextChip(
        context_type=ContextType.RECOMMENDATION,
        account_id="acc-1",
        entity_id="r-1",
        context_version="cv-2",
        recommendation_version="rv-5",
    )
    res = resolve(ResolveRequest(intent=IntentClass.REVIEW_ACTION, active_context=chip))
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.context_version == "cv-2"
    assert res.chip.recommendation_version == "rv-5"


def test_active_context_card_leading_missing_context_version_fails_closed() -> None:
    """A stale active chip (version dropped on re-fetch) must not lead a card."""
    chip = ContextChip(context_type=ContextType.PRODUCT, account_id="acc-1", entity_id="e-9")
    res = resolve(ResolveRequest(intent=IntentClass.PREPARE_ACTION, active_context=chip))
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_context_version"


def test_active_context_recommendation_missing_recommendation_version_fails_closed() -> None:
    chip = ContextChip(
        context_type=ContextType.RECOMMENDATION,
        account_id="acc-1",
        entity_id="r-1",
        context_version="cv-2",
    )
    res = resolve(ResolveRequest(intent=IntentClass.PREPARE_ACTION, active_context=chip))
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.reason == "missing_recommendation_version"


def test_active_context_question_without_version_still_resolves() -> None:
    """A read-only Question resolves against a versionless chip (no card bound)."""
    chip = ContextChip(context_type=ContextType.PRODUCT, account_id="acc-1", entity_id="e-9")
    res = resolve(ResolveRequest(intent=IntentClass.QUESTION, active_context=chip))
    assert res.kind is ResolutionKind.RESOLVED


def test_exactly_one_active_context_on_resolve() -> None:
    """A RESOLVED outcome yields exactly one chip; a PICKER yields none."""
    resolved = resolve(
        ResolveRequest(
            intent=IntentClass.QUESTION,
            active_context=ContextChip(context_type=ContextType.PRODUCT, entity_id="e"),
        )
    )
    assert resolved.chip is not None and resolved.options == []
    picker = resolve(ResolveRequest(intent=IntentClass.QUESTION))
    assert picker.chip is None


# --- time-range resolution ---------------------------------------------------


@pytest.mark.parametrize(
    ("phrase", "expected_start", "expected_label"),
    [
        ("today", "2026-07-17T00:00:00Z", "time.range.today"),
        ("امروز", "2026-07-17T00:00:00Z", "time.range.today"),
        ("yesterday", "2026-07-16T00:00:00Z", "time.range.yesterday"),
        ("دیروز", "2026-07-16T00:00:00Z", "time.range.yesterday"),
        ("last 7 days", "2026-07-11T00:00:00Z", "time.range.last_n_days"),
        ("۷ روز گذشته", "2026-07-11T00:00:00Z", "time.range.last_n_days"),
        ("past 5 days", "2026-07-13T00:00:00Z", "time.range.last_n_days"),
        ("something odd", "2026-07-17T00:00:00Z", "time.range.unspecified"),
    ],
)
def test_time_range_is_explicit_with_as_of(
    phrase: str, expected_start: str, expected_label: str
) -> None:
    now = "2026-07-17T09:30:00Z"
    tr = resolve_time_range(phrase, now)
    # Always an explicit closed range plus an as-of instant (§8.1).
    assert tr.start == expected_start
    assert tr.end == now
    assert tr.as_of == now
    assert tr.label_key == expected_label


def test_resolve_attaches_time_range_when_phrase_present() -> None:
    req = ResolveRequest(
        intent=IntentClass.QUESTION,
        active_context=ContextChip(context_type=ContextType.PRODUCT, entity_id="e"),
        time_phrase="۷ روز گذشته",
        now="2026-07-17T09:30:00Z",
    )
    res = resolve(req)
    assert res.time_range is not None
    assert res.time_range.as_of == "2026-07-17T09:30:00Z"


# --- fixture-backed containment (CHAT-007) -----------------------------------


def _load(path: Path) -> list[dict[str, Any]]:
    with path.open(encoding="utf-8") as fh:
        return [json.loads(line) for line in fh if line.strip()]


def _request_from_case(case: dict[str, Any]) -> ResolveRequest:
    return ResolveRequest.model_validate(
        {
            "intent": case["intent"],
            "account_id": "acc-1",
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

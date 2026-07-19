"""Entity-reference normalization in context resolution (#29, S21, CHAT-081).

A reference token and the candidate-map keys are matched on their BOUNDARY-
canonical form, so a Persian/Arabic-Indic-digit or Arabic-Kaf/Yeh spelling of an
identifier resolves IDENTICALLY to its Latin/Persian-canonical twin. Raw tokens
are preserved as evidence (never mutated). A canonicalization COLLISION — two or
more distinct raw candidate keys folding to the same key — yields a structured
PICKER, never an arbitrary pick and never NOT_FOUND. Every existing tenant-scope
and card-version fail-closed guard still fires over the merged candidate set.

Negative/equivalence-first per the repo TDD contract: these assert the folded
forms resolve the same and that collisions picker, before the happy Latin path.
"""

from __future__ import annotations

import random

from llm.contextres import (
    ContextType,
    EntityCandidate,
    EntityRef,
    RequestScope,
    ResolutionKind,
    ResolveRequest,
    resolve,
)
from llm.intents.models import IntentClass

_ORG = "org-1"
_ACCOUNT = "acc-1"
_SCOPE = RequestScope(organization_id=_ORG, account_id=_ACCOUNT)
_OTHER_ACCOUNT = "acc-2"

_PERSIAN_ZERO = 0x06F0
_ARABIC_ZERO = 0x0660
_ZWNJ = "‌"


def _ref(raw: str, ctype: ContextType = ContextType.PRODUCT) -> EntityRef:
    return EntityRef(context_type=ctype, entity_id="", raw=raw)


def _cand(
    eid: str,
    raw: str,
    *,
    account_id: str | None = _ACCOUNT,
    context_version: str | None = "cv-1",
    ctype: ContextType = ContextType.PRODUCT,
) -> EntityCandidate:
    return EntityCandidate(
        context_type=ctype,
        entity_id=eid,
        raw=raw,
        organization_id=_ORG,
        account_id=account_id,
        context_version=context_version,
    )


def _to_family(latin: str, zero_code: int) -> str:
    return "".join(chr(zero_code + (ord(c) - 0x30)) if c.isdigit() else c for c in latin)


# --- digit-family equivalence (the reported bug) -----------------------------


def test_persian_digit_reference_matches_latin_candidate_key() -> None:
    """`SKU-۱۲۳` (Persian digits) resolves to the canonical `SKU-123` candidate."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref("SKU-۱۲۳")],  # SKU-۱۲۳
        candidates={"SKU-123": [_cand("p-123", "SKU-123")]},
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.entity_id == "p-123"
    assert res.reason == "explicit_reference_override"


def test_arabic_indic_digit_reference_matches_latin_candidate_key() -> None:
    """`SKU-١٢٣` (Arabic-Indic digits) resolves to the canonical `SKU-123`."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref("SKU-١٢٣")],  # SKU-١٢٣
        candidates={"SKU-123": [_cand("p-123", "SKU-123")]},
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None
    assert res.chip.entity_id == "p-123"


def test_all_digit_families_resolve_identically() -> None:
    """Latin, Persian, and Arabic-Indic spellings of one id resolve to one chip."""
    latin = "SKU-123"
    for raw in (latin, _to_family(latin, _PERSIAN_ZERO), _to_family(latin, _ARABIC_ZERO)):
        res = resolve(
            ResolveRequest(
                intent=IntentClass.PREPARE_ACTION,
                scope=_SCOPE,
                references=[_ref(raw)],
                candidates={latin: [_cand("p-123", latin)]},
            )
        )
        assert res.kind is ResolutionKind.RESOLVED, raw
        assert res.chip is not None and res.chip.entity_id == "p-123", raw


def test_reference_raw_is_not_mutated_by_matching() -> None:
    """Raw evidence survives: matching folds a copy, never the token itself."""
    raw = "SKU-۱۲۳"
    ref = _ref(raw)
    resolve(
        ResolveRequest(
            intent=IntentClass.QUESTION,
            scope=_SCOPE,
            references=[ref],
            candidates={"SKU-123": [_cand("p-123", "SKU-123", context_version=None)]},
        )
    )
    assert ref.raw == raw  # unchanged


# --- character / diacritic equivalence ---------------------------------------


def test_arabic_kaf_yeh_reference_matches_persian_candidate_key() -> None:
    """An identifier typed with Arabic Kaf/Yeh matches its Persian-glyph key."""
    persian_key = "کیف-1"  # کیف-1 (Persian keheh + yeh)
    arabic_ref = "كيف-1"  # كيف-1 (Arabic kaf + yeh)
    res = resolve(
        ResolveRequest(
            intent=IntentClass.PREPARE_ACTION,
            scope=_SCOPE,
            references=[_ref(arabic_ref)],
            candidates={persian_key: [_cand("p-k", persian_key)]},
        )
    )
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None and res.chip.entity_id == "p-k"


def test_zwnj_in_reference_is_folded_for_matching() -> None:
    """A stray ZWNJ inside an identifier does not defeat the match."""
    res = resolve(
        ResolveRequest(
            intent=IntentClass.PREPARE_ACTION,
            scope=_SCOPE,
            references=[_ref(f"SKU-1{_ZWNJ}23")],
            candidates={"SKU-123": [_cand("p-123", "SKU-123")]},
        )
    )
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None and res.chip.entity_id == "p-123"


def test_folded_candidate_key_matches_latin_reference() -> None:
    """Folding is symmetric: a Persian-digit candidate KEY matches a Latin ref."""
    res = resolve(
        ResolveRequest(
            intent=IntentClass.PREPARE_ACTION,
            scope=_SCOPE,
            references=[_ref("SKU-123")],
            candidates={"SKU-۱۲۳": [_cand("p-123", "SKU-۱۲۳")]},
        )
    )
    assert res.kind is ResolutionKind.RESOLVED
    assert res.chip is not None and res.chip.entity_id == "p-123"


# --- collision handling: distinct raw keys that fold together ⇒ PICKER --------


def test_canonical_key_collision_produces_picker_not_arbitrary_pick() -> None:
    """Two DISTINCT raw keys folding to the reference's key ⇒ union PICKER."""
    req = ResolveRequest(
        intent=IntentClass.PREPARE_ACTION,
        scope=_SCOPE,
        references=[_ref("SKU-123")],
        candidates={
            "SKU-123": [_cand("p-latin", "SKU-123")],
            "SKU-۱۲۳": [_cand("p-persian", "SKU-۱۲۳")],
        },
    )
    res = resolve(req)
    assert res.kind is ResolutionKind.PICKER
    assert res.chip is None  # never an arbitrary card subject
    assert res.reason == "canonical_key_collision"
    assert {o.entity_id for o in res.options} == {"p-latin", "p-persian"}


def test_collision_never_degrades_to_not_found() -> None:
    """A folded-key collision is ambiguity, not absence — never NOT_FOUND."""
    res = resolve(
        ResolveRequest(
            intent=IntentClass.QUESTION,
            scope=_SCOPE,
            references=[_ref("SKU-١٢٣")],  # Arabic-Indic
            candidates={
                "SKU-123": [_cand("a", "SKU-123", context_version=None)],
                "SKU-۱۲۳": [_cand("b", "SKU-۱۲۳", context_version=None)],
            },
        )
    )
    assert res.kind is ResolutionKind.PICKER
    assert res.reason == "canonical_key_collision"


def test_collision_with_out_of_scope_member_still_fails_closed() -> None:
    """Scope isolation is evaluated over the full merged set BEFORE collision:
    a cross-account member in a folded-key collision fails closed, not picker."""
    res = resolve(
        ResolveRequest(
            intent=IntentClass.PREPARE_ACTION,
            scope=_SCOPE,
            references=[_ref("SKU-123")],
            candidates={
                "SKU-123": [_cand("in-scope", "SKU-123")],
                "SKU-۱۲۳": [
                    _cand("foreign", "SKU-۱۲۳", account_id=_OTHER_ACCOUNT)
                ],
            },
        )
    )
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.chip is None
    assert res.options == []
    assert res.reason == "account_scope_mismatch"


# --- preserved behavior: single-key multi-candidate is ordinary ambiguity -----


def test_single_key_multiple_candidates_is_ambiguous_not_collision() -> None:
    """Two candidates under ONE key is the existing ambiguity path, not a
    canonical-key collision (which is about distinct KEYS folding together)."""
    res = resolve(
        ResolveRequest(
            intent=IntentClass.QUESTION,
            scope=_SCOPE,
            references=[_ref("کفش")],  # کفش
            candidates={
                "کفش": [
                    _cand("p1", "کفش", context_version=None),
                    _cand("p2", "کفش", context_version=None),
                ]
            },
        )
    )
    assert res.kind is ResolutionKind.PICKER
    assert res.reason == "ambiguous_reference"


def test_no_matching_key_still_not_found() -> None:
    """A reference whose folded form matches no candidate key fails closed."""
    res = resolve(
        ResolveRequest(
            intent=IntentClass.PREPARE_ACTION,
            scope=_SCOPE,
            references=[_ref("SKU-999")],
            candidates={"SKU-123": [_cand("p-123", "SKU-123")]},
        )
    )
    assert res.kind is ResolutionKind.NOT_FOUND
    assert res.reason == "reference_matched_nothing"


# --- property: mixed scripts / punctuation / digit families / idempotence -----


def test_property_digit_families_resolve_identically_over_sample() -> None:
    """A deterministic sample of identifiers resolves the same in every digit
    family, exercising mixed scripts, punctuation, and ZWNJ."""
    rng = random.Random(20260719)
    templates = ["SKU-{d}", "REF_{d}-A", "کد-{d}", f"ID{_ZWNJ}{{d}}", "P/{d}.{d}"]
    for _ in range(400):
        digits = "".join(rng.choice("0123456789") for _ in range(rng.randint(1, 8)))
        latin = rng.choice(templates).replace("{d}", digits)
        persian = _to_family(latin, _PERSIAN_ZERO)
        arabic = _to_family(latin, _ARABIC_ZERO)
        results = []
        for raw in (latin, persian, arabic):
            res = resolve(
                ResolveRequest(
                    intent=IntentClass.QUESTION,
                    scope=_SCOPE,
                    references=[_ref(raw)],
                    candidates={latin: [_cand("e-1", latin, context_version=None)]},
                )
            )
            assert res.kind is ResolutionKind.RESOLVED, raw
            assert res.chip is not None
            results.append(res.chip.entity_id)
        assert results == ["e-1", "e-1", "e-1"], latin

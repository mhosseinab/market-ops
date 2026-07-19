"""Number-WORD containment in grounding (issue #53, CHAT-002 / §4.6 free text).

The digit ban in ``grounding.py`` only sees digit glyphs; a quantity spelled OUT
in words (``ten percent`` / ``ده درصد`` / ``دویست تومان``) carries no digit and
would otherwise pass every model-visible free-text slot. These tests are written
negative-first: each asserts the spelled quantity is REJECTED with the right
distinct code, that the deliberate homograph exclusions (``یک``/``نه``) and
whole-token matching do NOT over-reject, and that the pre-existing digit ban is
untouched. A guard test proves no ``supported`` fixture newly trips.
"""

from __future__ import annotations

import pytest
from llm.envelope.contract import (
    Calculation,
    Claim,
    Comparison,
    InlineTable,
    Provenance,
    Recommendation,
    ResponseEnvelope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.grounding import find_violations
from llm.envelope.models import EvidenceRef, Money
from llm.evals.datasets import load_corpus

GOOD_EVIDENCE = EvidenceRef(
    evidence_id="ev-1", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
)
MARGIN_SRC = SourceRef(tool="read_contribution", response_field="contribution.total")


def _money_value() -> SourcedValue:
    return SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=990000, currency="IRR", exponent=0),
    )


def _codes(env: ResponseEnvelope) -> set[str]:
    return {v.code for v in find_violations(env)}


# --- model_inference slot ⇒ NUMBER_WORD_IN_MODEL_TEXT -----------------------


@pytest.mark.parametrize(
    "text",
    [
        "drop the price by ten percent",  # EN cardinal
        "قیمت را ده درصد کم کن",  # FA cardinal ده (ten)
        "دویست تومان بالاتر از کف است",  # FA atomic hundreds دویست (200)
        "سیصد ریال پایین‌تر",  # FA atomic hundreds سیصد (300)
        "the twentieth listing is cheapest",  # EN ordinal
    ],
)
def test_number_word_in_model_text_is_rejected(text: str) -> None:
    env = ResponseEnvelope(model_inference=text)
    assert "NUMBER_WORD_IN_MODEL_TEXT" in _codes(env)


def test_model_text_number_word_uses_the_model_specific_code() -> None:
    env = ResponseEnvelope(model_inference="about fifty units remain")
    codes = _codes(env)
    assert "NUMBER_WORD_IN_MODEL_TEXT" in codes
    assert "NUMBER_WORD_IN_TEXT" not in codes  # not the ordinary-slot code


# --- ordinary slots ⇒ NUMBER_WORD_IN_TEXT -----------------------------------


def test_number_word_in_claim_statement_is_rejected() -> None:
    env = ResponseEnvelope(
        observed_facts=[Claim(statement="ten sellers observed", evidence=[GOOD_EVIDENCE])]
    )
    codes = _codes(env)
    assert "NUMBER_WORD_IN_TEXT" in codes
    assert "NUMBER_WORD_IN_MODEL_TEXT" not in codes


def test_number_word_in_recommendation_statement_is_rejected() -> None:
    env = ResponseEnvelope(
        recommendation=Recommendation(
            statement="lower price by twenty percent", deep_link="/app/rec"
        )
    )
    assert "NUMBER_WORD_IN_TEXT" in _codes(env)


def test_number_word_in_calculation_label_is_rejected() -> None:
    env = ResponseEnvelope(
        deterministic_calculations=[
            Calculation(label="contribution for five units", result=_money_value(),
                        evidence=[GOOD_EVIDENCE])
        ]
    )
    assert "NUMBER_WORD_IN_TEXT" in _codes(env)


def test_number_word_in_comparison_label_is_rejected() -> None:
    env = ResponseEnvelope(
        comparisons=[Comparison(label="move over three days", left=_money_value(),
                                right=_money_value(), delta=_money_value(),
                                left_captured_at="2026-07-16T10:00:00Z",
                                right_captured_at="2026-07-17T10:00:00Z")]
    )
    assert "NUMBER_WORD_IN_TEXT" in _codes(env)


def test_number_word_in_missing_data_is_rejected() -> None:
    env = ResponseEnvelope(missing_data=["three competitor offers"])
    assert "NUMBER_WORD_IN_TEXT" in _codes(env)


def test_number_word_in_table_cell_is_rejected() -> None:
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "count"], rows=[["A", "seven"]],
                            total_row_count=1)]
    )
    assert "NUMBER_WORD_IN_TEXT" in _codes(env)


# --- finding (2): FA atomic hundreds must all be caught ---------------------


@pytest.mark.parametrize(
    "word", ["دویست", "سیصد", "چهارصد", "پانصد", "ششصد", "هفتصد", "هشتصد", "نهصد"]
)
def test_fa_atomic_hundreds_are_rejected(word: str) -> None:
    env = ResponseEnvelope(model_inference=f"{word} تومان بالاتر از کف")
    assert "NUMBER_WORD_IN_MODEL_TEXT" in _codes(env)


# --- finding (1): Arabic-glyph spellings fold before matching ----------------


def test_arabic_glyph_number_word_is_rejected_like_persian() -> None:
    """``دويست`` (Arabic Yeh U+064A) must reject exactly like ``دویست`` (Persian).

    Combines finding (1) (Arabic-glyph fold) with finding (2) (atomic hundreds):
    the char fold reused from ``intents.normalize`` maps ي→ی so the token matches.
    """
    persian = ResponseEnvelope(model_inference="دویست تومان بالاتر از کف")
    arabic = ResponseEnvelope(model_inference="دويست تومان بالاتر از کف")  # Arabic ي
    assert "NUMBER_WORD_IN_MODEL_TEXT" in _codes(persian)
    assert "NUMBER_WORD_IN_MODEL_TEXT" in _codes(arabic)


def test_arabic_glyph_teen_number_word_is_rejected() -> None:
    # یازده (eleven) written with Arabic Yeh: يازده.
    env = ResponseEnvelope(model_inference="يازده فروشنده")
    assert "NUMBER_WORD_IN_MODEL_TEXT" in _codes(env)


# --- finding (3): deliberate homograph exclusions must NOT over-reject -------


def test_persian_indefinite_article_yek_is_accepted() -> None:
    """``یک`` as the article "a/an" must NOT trip (decided exclusion, finding 3)."""
    env = ResponseEnvelope(
        observed_facts=[Claim(statement="یک پیشنهاد معتبر روی این کالا مشاهده شد",
                              evidence=[GOOD_EVIDENCE], state_key="state.verified")]
    )
    codes = _codes(env)
    assert "NUMBER_WORD_IN_TEXT" not in codes
    assert "NUMBER_WORD_IN_MODEL_TEXT" not in codes


def test_persian_negator_nah_is_accepted() -> None:
    """``نه`` as the negator "no/not" must NOT trip (decided exclusion, finding 3)."""
    env = ResponseEnvelope(model_inference="نه، این پیشنهاد از پنجرهٔ تازگی گذشته است")
    assert "NUMBER_WORD_IN_MODEL_TEXT" not in _codes(env)


def test_arabic_glyph_yek_stays_excluded_after_fold() -> None:
    """``يك`` (Arabic) folds to ``یک`` which is the excluded article — still accepted.

    The digit forms ``۱``/``1`` remain caught by the digit ban; only the spelled
    article is intentionally let through (finding 3 residual-coverage note).
    """
    env = ResponseEnvelope(model_inference="يك پیشنهاد معتبر مشاهده شد")  # Arabic ي/ك
    assert "NUMBER_WORD_IN_MODEL_TEXT" not in _codes(env)


# --- finding (4): ordinals (defense-in-depth) --------------------------------


@pytest.mark.parametrize(
    "text",
    [
        "the eleventh offer",  # EN ordinal
        "the hundredth listing",  # EN ordinal
        "چهارم در فهرست",  # FA ordinal (fourth)
        "رتبهٔ نهم",  # FA ordinal (ninth) — نهم is listed even though نه is excluded
        "یکم بودن در باکس خرید",  # FA ordinal (first) — یکم listed though یک excluded
    ],
)
def test_ordinals_are_rejected(text: str) -> None:
    env = ResponseEnvelope(model_inference=text)
    assert "NUMBER_WORD_IN_MODEL_TEXT" in _codes(env)


# --- whole-token only: a word CONTAINING a number word is NOT a trip --------


@pytest.mark.parametrize(
    "text",
    [
        "we often review the offer before deciding",  # 'often' ⊃ 'ten'
        "someone configured the floor for this listing",  # 'someone' ⊃ 'one'
        "نرخ به درصد نمایش داده می‌شود",  # 'درصد' ⊃ 'صد' (percent contains hundred)
        "این کالا سود خوبی دارد",  # 'سود' near 'صد'? distinct token, no trip
    ],
)
def test_substring_of_number_word_is_not_rejected(text: str) -> None:
    env = ResponseEnvelope(model_inference=text)
    codes = _codes(env)
    assert "NUMBER_WORD_IN_TEXT" not in codes
    assert "NUMBER_WORD_IN_MODEL_TEXT" not in codes


# --- the digit ban is fully intact (regression guard) -----------------------


def test_digit_ban_still_fires_independently() -> None:
    env = ResponseEnvelope(model_inference="price is 990000 IRR")
    assert "NUMBER_IN_MODEL_TEXT" in _codes(env)


def test_digit_and_word_both_fire_on_one_field() -> None:
    env = ResponseEnvelope(model_inference="ten sellers at 990000 IRR")
    codes = _codes(env)
    assert "NUMBER_IN_MODEL_TEXT" in codes  # digit layer intact
    assert "NUMBER_WORD_IN_MODEL_TEXT" in codes  # word layer added


def test_clean_prose_has_neither_code() -> None:
    env = ResponseEnvelope(
        model_inference="The offer sits just above your configured floor."
    )
    codes = _codes(env)
    assert "NUMBER_WORD_IN_MODEL_TEXT" not in codes
    assert "NUMBER_IN_MODEL_TEXT" not in codes


# --- MANDATORY guard: no ``supported`` fixture newly over-rejects ------------


def test_no_supported_fixture_trips_a_number_word() -> None:
    """Every model-visible slot of every ``supported`` factual fixture must be
    clean under the deny-list — proving the deny-list did not over-reject
    legitimate supported content (issue #53 fixture-realignment guard)."""
    corpus = load_corpus()
    offenders: list[str] = []
    for row in corpus.factual():
        if row.get("expected") != "supported":
            continue
        slots: list[str] = [str(row.get("model_inference", ""))]
        for section in ("observed_facts", "dk_signals", "seller_config"):
            for claim in row.get(section, []):
                slots.append(str(claim.get("statement", "")))
        rec = row.get("recommendation")
        if rec:
            slots.append(str(rec.get("statement", "")))
        for calc in row.get("calculations", []):
            slots.append(str(calc.get("label", "")))
        for cmp in row.get("comparisons", []):
            slots.append(str(cmp.get("label", "")))
        for note in row.get("missing_data", []):
            slots.append(str(note))
        for table in row.get("tables", []):
            slots.extend(str(c) for c in table.get("columns", []))
            for r in table.get("rows", []):
                slots.extend(str(c) for c in r)
            if table.get("summary"):
                slots.append(str(table["summary"]))
        env = ResponseEnvelope(model_inference="\n".join(slots))
        codes = {v.code for v in find_violations(env)}
        if "NUMBER_WORD_IN_MODEL_TEXT" in codes:
            offenders.append(str(row["id"]))
    assert not offenders, f"supported fixtures newly over-rejected: {offenders}"

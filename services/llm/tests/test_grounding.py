"""Per-rule grounding tests (§12.2 / CHAT-002/005/012/021/022/023).

Each rule gets an ADVERSARIAL fixture that MUST be rejected: a fabricated number
with no source, an operational claim with no evidence, a >20-row table, an
invented state term, a number smuggled into model text, an incomplete
comparison, an exposure total not from the margin engine, and a calculation not
from an engine. A grounded envelope with all seven §12.2 kinds is accepted.
"""

from __future__ import annotations

import pytest
from llm.envelope.contract import (
    Calculation,
    Claim,
    Comparison,
    ComparisonKind,
    ComparisonRelation,
    ExposureTotal,
    InlineTable,
    Provenance,
    Recommendation,
    ResponseEnvelope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.grounding import (
    GroundingError,
    find_violations,
    validate_grounding,
)
from llm.envelope.models import EvidenceRef, Money, RawEvidenceValue

# --- reusable grounded building blocks --------------------------------------

GOOD_EVIDENCE = EvidenceRef(
    evidence_id="ev-1", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
)
MARGIN_SRC = SourceRef(tool="read_contribution", response_field="contribution.total")
DK_SRC = SourceRef(tool="read_observation", response_field="offer.seller_count")


def _money_value() -> SourcedValue:
    return SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=990000, currency="IRR", exponent=0),
    )


def _count_value() -> SourcedValue:
    return SourcedValue(source=DK_SRC, provenance=Provenance.DK_SIGNAL, count=4)


def _money_of(mantissa: int) -> SourcedValue:
    """A margin-engine IRR money operand at exponent 0 with the given mantissa."""
    return SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=mantissa, currency="IRR", exponent=0),
    )


def _coherent_comparison(**overrides: object) -> Comparison:
    """A genuinely coherent temporal comparison: 990000 → 880000, Δ 110000, decrease.

    left − right = 990000 − 880000 = 110000 (exact integer), same currency/exponent,
    same entity across both readings, relation DECREASE (right < left). Every issue
    #55 coherence check passes; individual tests override a single field to isolate
    one failure.
    """
    defaults: dict[str, object] = {
        "label": "price vs last read",
        "left": _money_of(990000),
        "right": _money_of(880000),
        "delta": _money_of(110000),
        "left_captured_at": "2026-07-16T10:00:00Z",
        "right_captured_at": "2026-07-17T10:00:00Z",
        "kind": ComparisonKind.TEMPORAL,
        "relation": ComparisonRelation.DECREASE,
        "left_entity": "sku-1",
        "right_entity": "sku-1",
    }
    defaults.update(overrides)
    return Comparison(**defaults)  # type: ignore[arg-type]


def _codes(env: ResponseEnvelope) -> set[str]:
    return {v.code for v in find_violations(env)}


# --- happy path: a fully grounded, seven-kind envelope ----------------------


def test_grounded_envelope_with_all_seven_kinds_validates() -> None:
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="lowest qualifying offer observed", evidence=[GOOD_EVIDENCE],
                  value=SourcedValue(
                      source=SourceRef(tool="read_observation", response_field="offer.price"),
                      provenance=Provenance.OBSERVED,
                      raw=RawEvidenceValue(raw="۹۹۰٬۰۰۰ تومان", unit="toman"))),
        ],
        dk_signals=[Claim(statement="seller count", evidence=[GOOD_EVIDENCE],
                          value=_count_value(), state_key="state.supported")],
        seller_config=[Claim(statement="floor configured", evidence=[GOOD_EVIDENCE],
                             state_key="readiness.complete")],
        deterministic_calculations=[
            Calculation(label="contribution", result=_money_value(), evidence=[GOOD_EVIDENCE]),
        ],
        model_inference="The lowest qualifying offer sits just above your floor.",
        missing_data=["competitor buy-box share"],
        recommendation=Recommendation(
            statement="review a price adjustment", deep_link="/app/recommendation/1",
            state_key="state.simulation"),
        comparisons=[_coherent_comparison()],
        tables=[InlineTable(columns=["sku", "price"], rows=[["A", "x"]], total_row_count=1)],
        exposure=ExposureTotal(known=True, total=_money_value()),
    )
    validate_grounding(env)  # does not raise
    assert find_violations(env) == []


# --- CHAT-002: fabricated number (no source reference) ----------------------


def test_fabricated_number_without_source_is_rejected() -> None:
    fabricated = SourcedValue(
        source=SourceRef(tool="", response_field=""),  # empty ⇒ not present
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=123456, currency="IRR"),
    )
    env = ResponseEnvelope(
        observed_facts=[Claim(statement="made-up price", evidence=[GOOD_EVIDENCE],
                              value=fabricated)],
    )
    assert "FABRICATED_NUMBER" in _codes(env)
    with pytest.raises(GroundingError):
        validate_grounding(env)


# --- CHAT-002: a number smuggled into model text ----------------------------


@pytest.mark.parametrize("text", ["price is 990000 IRR", "قیمت ۹۹۰۰۰۰ ریال", "٤ فروشنده"])
def test_number_in_model_text_is_rejected(text: str) -> None:
    env = ResponseEnvelope(model_inference=text)
    assert "NUMBER_IN_MODEL_TEXT" in _codes(env)


def test_model_text_without_digits_is_allowed() -> None:
    env = ResponseEnvelope(model_inference="The offer sits just above your configured floor.")
    assert "NUMBER_IN_MODEL_TEXT" not in _codes(env)


# --- CHAT-005: operational claim with no evidence ---------------------------


def test_claim_without_evidence_is_rejected() -> None:
    env = ResponseEnvelope(dk_signals=[Claim(statement="seller count is high", evidence=[])])
    assert "MISSING_EVIDENCE" in _codes(env)


def test_claim_with_malformed_evidence_is_rejected() -> None:
    bad = EvidenceRef(evidence_id="ev-2", captured_at="", quality="state.verified")
    env = ResponseEnvelope(observed_facts=[Claim(statement="stale reading", evidence=[bad])])
    assert "MISSING_EVIDENCE" in _codes(env)


# --- CHAT-023: inline table over the 20-row cap -----------------------------


def test_table_over_20_rows_is_rejected() -> None:
    rows = [[str(i), "x"] for i in range(21)]
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "price"], rows=rows, total_row_count=21)]
    )
    assert "TABLE_OVERFLOW" in _codes(env)


def test_table_exactly_20_rows_is_allowed() -> None:
    rows = [[str(i), "x"] for i in range(20)]
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "price"], rows=rows, total_row_count=20)]
    )
    assert "TABLE_OVERFLOW" not in _codes(env)


def test_table_with_more_total_rows_must_summarize_and_deep_link() -> None:
    rows = [["item", "x"] for _ in range(20)]  # digit-free cells
    # 200 total, only 20 shown, but no summary/deep-link ⇒ rejected.
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "price"], rows=rows, total_row_count=200)]
    )
    assert "TABLE_NOT_SUMMARIZED" in _codes(env)
    ok = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "price"], rows=rows, total_row_count=200,
                            summary="showing a partial slice", deep_link="/app/products")]
    )
    assert _codes(ok) == set()


# --- CHAT-022: invented (non-canonical) state term --------------------------


def test_non_canonical_state_term_is_rejected() -> None:
    env = ResponseEnvelope(
        seller_config=[Claim(statement="status", evidence=[GOOD_EVIDENCE],
                             state_key="state.awesome")]
    )
    assert "NON_CANONICAL_STATE" in _codes(env)


def test_canonical_state_term_is_allowed() -> None:
    env = ResponseEnvelope(
        seller_config=[Claim(statement="status", evidence=[GOOD_EVIDENCE],
                             state_key="state.blocked")]
    )
    assert "NON_CANONICAL_STATE" not in _codes(env)


# --- CHAT-021: comparison missing a timestamp -------------------------------


def test_comparison_missing_timestamp_is_rejected() -> None:
    # Operands are otherwise coherent, so COMPARISON_INCOMPLETE fires in isolation.
    env = ResponseEnvelope(
        comparisons=[_coherent_comparison(right_captured_at="")]
    )
    assert "COMPARISON_INCOMPLETE" in _codes(env)


# --- Issue #55: comparison numeric coherence --------------------------------
# A comparison is a STRUCTURAL relation, not three independently-sourced numbers.
# Every one of these fixtures reuses individually-present numbers but recombines
# them into a claim the evidence does not support; each MUST be rejected.


def test_incorrect_delta_is_rejected() -> None:
    # Verification bullet 1: delta != left − right (990000 − 880000 = 110000, not
    # 990000). Every literal number is individually sourced, yet the relationship
    # is false.
    env = ResponseEnvelope(
        comparisons=[_coherent_comparison(delta=_money_of(990000))]
    )
    codes = _codes(env)
    assert "COMPARISON_DELTA_INCOHERENT" in codes
    with pytest.raises(GroundingError):
        validate_grounding(env)


def test_swapped_operands_flip_delta_sign_and_are_rejected() -> None:
    # Verification bullet 2: swapping left/right flips the true sign of left−right
    # (now 880000 − 990000 = −110000) so the stated +110000 delta no longer holds,
    # and the DECREASE relation no longer agrees either.
    env = ResponseEnvelope(
        comparisons=[
            _coherent_comparison(left=_money_of(880000), right=_money_of(990000))
        ]
    )
    codes = _codes(env)
    assert "COMPARISON_DELTA_INCOHERENT" in codes
    assert "COMPARISON_RELATION_MISMATCH" in codes


def test_mixed_currency_operands_are_rejected() -> None:
    # Verification bullet 3a: left IRR, right USD — mantissas are individually
    # sourced but not comparable; the arithmetic is meaningless.
    usd = SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=880000, currency="USD", exponent=0),
    )
    env = ResponseEnvelope(comparisons=[_coherent_comparison(right=usd)])
    assert "COMPARISON_UNIT_MISMATCH" in _codes(env)


def test_mixed_exponent_operands_are_rejected() -> None:
    # Verification bullet 3b: same currency, different exponent — the mantissas are
    # not at a shared scale, so a raw mantissa subtraction would be wrong.
    scaled = SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=88000, currency="IRR", exponent=1),
    )
    env = ResponseEnvelope(comparisons=[_coherent_comparison(right=scaled)])
    assert "COMPARISON_UNIT_MISMATCH" in _codes(env)


def test_money_vs_count_operands_are_rejected() -> None:
    # Verification bullet 3c: a money operand compared against a bare count.
    env = ResponseEnvelope(comparisons=[_coherent_comparison(right=_count_value())])
    assert "COMPARISON_UNIT_MISMATCH" in _codes(env)


def test_raw_operand_is_rejected_as_non_numeric() -> None:
    # A comparison operand must be an authoritative number, never a raw evidence
    # string (kept rejected as before, issue #55 unit/type coherence).
    raw = SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        raw=RawEvidenceValue(raw="۸۸۰٬۰۰۰ تومان", unit="toman"),
    )
    env = ResponseEnvelope(comparisons=[_coherent_comparison(right=raw)])
    assert "COMPARISON_UNIT_MISMATCH" in _codes(env)


def test_temporal_comparison_with_mismatched_entities_is_rejected() -> None:
    # Verification bullet 4: a before/after (temporal) comparison whose two
    # readings name DIFFERENT entities is incoherent — a delta across two products
    # is not a trend for either.
    env = ResponseEnvelope(
        comparisons=[_coherent_comparison(left_entity="sku-1", right_entity="sku-2")]
    )
    assert "COMPARISON_ENTITY_MISMATCH" in _codes(env)


def test_cross_entity_comparison_with_same_entity_is_rejected() -> None:
    # Verification bullet 4 (mirror): an A/B cross-entity comparison whose two
    # operands are the SAME entity is not an A/B comparison at all.
    env = ResponseEnvelope(
        comparisons=[
            _coherent_comparison(
                kind=ComparisonKind.CROSS_ENTITY,
                left_entity="sku-1",
                right_entity="sku-1",
            )
        ]
    )
    assert "COMPARISON_ENTITY_MISMATCH" in _codes(env)


def test_unbound_empty_entity_is_rejected() -> None:
    # Verification bullet 5: an operand with no bound entity identifier.
    env = ResponseEnvelope(
        comparisons=[_coherent_comparison(right_entity="   ")]
    )
    assert "COMPARISON_UNBOUND_ENTITY" in _codes(env)


def test_relation_disagreeing_with_signed_delta_is_rejected() -> None:
    # Verification bullet 4/relation: numbers say the price fell (right < left) but
    # the claim asserts INCREASE.
    env = ResponseEnvelope(
        comparisons=[_coherent_comparison(relation=ComparisonRelation.INCREASE)]
    )
    assert "COMPARISON_RELATION_MISMATCH" in _codes(env)


def test_coherent_count_comparison_is_accepted() -> None:
    # A non-money (count) comparison is also coherently checkable: 6 → 4, Δ 2.
    def _count(mantissa: int) -> SourcedValue:
        return SourcedValue(source=DK_SRC, provenance=Provenance.DK_SIGNAL, count=mantissa)

    env = ResponseEnvelope(
        comparisons=[
            _coherent_comparison(
                left=_count(6), right=_count(4), delta=_count(2),
                relation=ComparisonRelation.DECREASE,
            )
        ]
    )
    assert _codes(env) == set()


def test_coherent_comparison_is_accepted() -> None:
    # Verification bullet 6: the genuinely coherent comparison passes cleanly.
    env = ResponseEnvelope(comparisons=[_coherent_comparison()])
    assert _codes(env) == set()
    validate_grounding(env)  # does not raise


# --- CHAT-012: exposure total not from the margin engine --------------------


def test_exposure_total_not_from_margin_engine_is_rejected() -> None:
    not_margin = SourcedValue(
        source=SourceRef(tool="read_observation", response_field="offer.price"),
        provenance=Provenance.PRICING_ENGINE,
        money=Money(mantissa=500000, currency="IRR"),
    )
    env = ResponseEnvelope(exposure=ExposureTotal(known=True, total=not_margin))
    assert "EXPOSURE_NOT_FROM_MARGIN_ENGINE" in _codes(env)


def test_unknown_exposure_renders_unknown_without_violation() -> None:
    env = ResponseEnvelope(exposure=ExposureTotal.unknown())
    assert _codes(env) == set()


# --- §12.3: deterministic calculation not from an engine --------------------


def test_calculation_not_from_engine_is_rejected() -> None:
    observed = SourcedValue(
        source=SourceRef(tool="read_observation", response_field="offer.price"),
        provenance=Provenance.OBSERVED,
        money=Money(mantissa=500000, currency="IRR"),
    )
    env = ResponseEnvelope(
        deterministic_calculations=[Calculation(label="contribution", result=observed,
                                                 evidence=[GOOD_EVIDENCE])]
    )
    assert "CALCULATION_NOT_FROM_ENGINE" in _codes(env)


# --- CHAT-002: a number moved one field over is STILL rejected --------------
# The model_inference digit-ban must not be bypassable by relocating the digit
# into any other model-visible free-text slot.


def test_digit_in_table_cell_is_rejected() -> None:
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "price"], rows=[["B", "1250000 IRR"]],
                            total_row_count=1)]
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_persian_digit_in_table_cell_is_rejected() -> None:
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "price"], rows=[["A", "۹۹۰٬۰۰۰ تومان"]],
                            total_row_count=1)]
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_digit_in_claim_statement_is_rejected() -> None:
    env = ResponseEnvelope(
        observed_facts=[Claim(statement="the price is 990000 IRR", evidence=[GOOD_EVIDENCE])]
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_digit_in_recommendation_statement_is_rejected() -> None:
    env = ResponseEnvelope(
        recommendation=Recommendation(statement="drop price by 15000 IRR",
                                      deep_link="/app/recommendation")
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_digit_in_table_summary_is_rejected() -> None:
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku", "value"], rows=[["A", "x"]], total_row_count=200,
                            summary="990000 more rows", deep_link="/app/products")]
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_digit_in_calculation_label_is_rejected() -> None:
    env = ResponseEnvelope(
        deterministic_calculations=[Calculation(label="contribution for 5 units",
                                                 result=_money_value(), evidence=[GOOD_EVIDENCE])]
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_digit_in_comparison_label_is_rejected() -> None:
    env = ResponseEnvelope(
        comparisons=[_coherent_comparison(label="price move over 2 days")]
    )
    assert "NUMBER_IN_TEXT" in _codes(env)


def test_digit_in_missing_data_entry_is_rejected() -> None:
    env = ResponseEnvelope(missing_data=["3 competitor offers"])
    assert "NUMBER_IN_TEXT" in _codes(env)


# --- CHAT-022: evidence quality must be a canonical catalog key -------------


def test_non_canonical_evidence_quality_is_rejected() -> None:
    bad = EvidenceRef(evidence_id="ev-3", captured_at="2026-07-17T10:00:00Z",
                      quality="totally_fresh")
    env = ResponseEnvelope(observed_facts=[Claim(statement="an offer", evidence=[bad])])
    assert "NON_CANONICAL_QUALITY" in _codes(env)


def test_canonical_evidence_quality_is_allowed() -> None:
    for quality in ("state.verified", "state.stale", "freshness.aging"):
        ev = EvidenceRef(evidence_id="ev-4", captured_at="2026-07-17T10:00:00Z", quality=quality)
        env = ResponseEnvelope(observed_facts=[Claim(statement="an offer", evidence=[ev])])
        assert "NON_CANONICAL_QUALITY" not in _codes(env)


# --- CHAT-023: total_row_count below the shown rows is a mismatch -----------


def test_total_row_count_below_shown_rows_is_rejected() -> None:
    env = ResponseEnvelope(
        tables=[InlineTable(columns=["sku"], rows=[["A"], ["B"], ["C"]], total_row_count=1)]
    )
    assert "TABLE_ROW_COUNT_MISMATCH" in _codes(env)

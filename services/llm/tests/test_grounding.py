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
        comparisons=[Comparison(
            label="price vs last read", left=_money_value(), right=_money_value(),
            delta=_money_value(), left_captured_at="2026-07-16T10:00:00Z",
            right_captured_at="2026-07-17T10:00:00Z")],
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
    env = ResponseEnvelope(
        comparisons=[Comparison(label="price move", left=_money_value(), right=_money_value(),
                                delta=_money_value(), left_captured_at="2026-07-16T10:00:00Z",
                                right_captured_at="")]
    )
    assert "COMPARISON_INCOMPLETE" in _codes(env)


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
        comparisons=[Comparison(label="price move over 2 days", left=_money_value(),
                                right=_money_value(), delta=_money_value(),
                                left_captured_at="2026-07-16T10:00:00Z",
                                right_captured_at="2026-07-17T10:00:00Z")]
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

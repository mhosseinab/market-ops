"""Section-evidence policy: minimum sources, required category, temporal coverage.

Issue #54 (§12.2 section semantics): schema-valid provenance is NOT enough. Each
response *section* declares, in one deterministic matrix
(:data:`llm.envelope.grounding.SECTION_POLICY`), how many evidence-backed sources
it needs, which evidence category its values may carry, and — for current-state
(temporal) sections — that it holds at least one current (non-stale) source unless
it is honestly disclosing staleness. A section that satisfies the shape but not
the semantics must FAIL CLOSED.

Negative (containment) tests come first (repo negative-tests-first rule): for every
policy-governed section type a missing/wrong category, an insufficient source
count, and (for temporal sections) expired-only evidence each fail closed; then a
valid mixed-source section for each passes.
"""

from __future__ import annotations

from llm.envelope.composer import compose_or_refuse
from llm.envelope.contract import (
    UNSCOPED,
    Calculation,
    CannotAnswer,
    Claim,
    Provenance,
    ResponseEnvelope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.grounding import SECTION_POLICY, find_violations
from llm.envelope.models import EvidenceRef, Money

# --- building blocks --------------------------------------------------------

FRESH = EvidenceRef(
    evidence_id="ev-1", captured_at="2026-07-17T10:00:00Z", quality="freshness.fresh"
)
CURRENT_VERIFIED = EvidenceRef(
    evidence_id="ev-2", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
)
STALE_FRESHNESS = EvidenceRef(
    evidence_id="ev-3", captured_at="2026-07-04T10:00:00Z", quality="freshness.stale"
)
STALE_STATE = EvidenceRef(
    evidence_id="ev-4", captured_at="2026-07-04T10:00:00Z", quality="state.stale"
)

OBS_SRC = SourceRef(tool="read_observation", response_field="offer.price")
DK_SRC = SourceRef(tool="read_event", response_field="sellers.count")
CFG_SRC = SourceRef(tool="read_config", response_field="floor.value")
MARGIN_SRC = SourceRef(tool="read_margin", response_field="contribution.total")


def _val(src: SourceRef, prov: Provenance) -> SourcedValue:
    return SourcedValue(source=src, provenance=prov, money=Money(mantissa=990000, currency="IRR"))


def _dk_val(prov: Provenance = Provenance.DK_SIGNAL) -> SourcedValue:
    return SourcedValue(source=DK_SRC, provenance=prov, count=3)


def _codes(env: ResponseEnvelope) -> set[str]:
    return {v.code for v in find_violations(env)}


# --- 0. the matrix is a real, deterministic table ---------------------------


def test_section_policy_matrix_is_declared() -> None:
    # The three §12.2 evidence-category sections are governed by the matrix.
    assert set(SECTION_POLICY) == {"observed_facts", "dk_signals", "seller_config"}
    for policy in SECTION_POLICY.values():
        assert policy.min_sources >= 1
        assert policy.allowed_provenances  # non-empty allowed set
    # observed_facts + dk_signals are the current-state (temporal) sections.
    assert SECTION_POLICY["observed_facts"].temporal is True
    assert SECTION_POLICY["dk_signals"].temporal is True
    assert SECTION_POLICY["seller_config"].temporal is False


# --- 1. NEGATIVE: a value of a category the section may not carry is rejected -


def test_observed_fact_with_engine_value_is_wrong_category() -> None:
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="an offer", evidence=[FRESH],
                  value=_val(MARGIN_SRC, Provenance.MARGIN_ENGINE))
        ]
    )
    assert "WRONG_EVIDENCE_CATEGORY" in _codes(env)


def test_dk_signal_with_observed_value_is_wrong_category() -> None:
    env = ResponseEnvelope(
        dk_signals=[
            Claim(statement="a signal", evidence=[FRESH], value=_dk_val(Provenance.OBSERVED))
        ]
    )
    assert "WRONG_EVIDENCE_CATEGORY" in _codes(env)


def test_seller_config_with_dk_value_is_wrong_category() -> None:
    env = ResponseEnvelope(
        seller_config=[Claim(statement="a config", evidence=[CURRENT_VERIFIED],
                             value=_val(CFG_SRC, Provenance.DK_SIGNAL))]
    )
    assert "WRONG_EVIDENCE_CATEGORY" in _codes(env)


def test_calculation_wrong_category_uses_existing_engine_code() -> None:
    # deterministic_calculations' required category is ENGINE — reuse the existing
    # dedicated check (compose, don't fork), not a second WRONG_EVIDENCE_CATEGORY.
    env = ResponseEnvelope(
        deterministic_calculations=[
            Calculation(label="contribution", evidence=[FRESH],
                        result=_val(OBS_SRC, Provenance.OBSERVED))
        ]
    )
    codes = _codes(env)
    assert "CALCULATION_NOT_FROM_ENGINE" in codes
    assert "WRONG_EVIDENCE_CATEGORY" not in codes


# --- 2. NEGATIVE: a present section with too few evidenced sources -----------


def test_observed_facts_insufficient_sources() -> None:
    env = ResponseEnvelope(observed_facts=[Claim(statement="an offer", evidence=[])])
    assert "INSUFFICIENT_SOURCES" in _codes(env)


def test_dk_signals_insufficient_sources() -> None:
    env = ResponseEnvelope(dk_signals=[Claim(statement="a signal", evidence=[])])
    assert "INSUFFICIENT_SOURCES" in _codes(env)


def test_seller_config_insufficient_sources() -> None:
    env = ResponseEnvelope(seller_config=[Claim(statement="a config", evidence=[])])
    assert "INSUFFICIENT_SOURCES" in _codes(env)


def test_calculation_insufficient_source_uses_existing_fabricated_code() -> None:
    # A calc's single required source is its engine result; an unsourced result is
    # already caught by FABRICATED_NUMBER (compose, don't fork).
    env = ResponseEnvelope(
        deterministic_calculations=[
            Calculation(label="contribution", evidence=[FRESH],
                        result=SourcedValue(source=SourceRef(tool="", response_field=""),
                                            provenance=Provenance.MARGIN_ENGINE,
                                            money=Money(mantissa=1, currency="IRR")))
        ]
    )
    assert "FABRICATED_NUMBER" in _codes(env)


def test_empty_section_has_no_source_requirement() -> None:
    # No claims ⇒ no minimum-source requirement (an absent section is legal).
    env = ResponseEnvelope(model_inference="a clean note")
    codes = _codes(env)
    assert "INSUFFICIENT_SOURCES" not in codes
    assert "STALE_TEMPORAL_EVIDENCE" not in codes
    assert "WRONG_EVIDENCE_CATEGORY" not in codes


# --- 3. NEGATIVE: temporal section with expired-only evidence ---------------


def test_observed_fact_stale_only_asserting_current_is_rejected() -> None:
    # Claims CURRENT validity (state.verified) but every source is stale.
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="the current offer", evidence=[STALE_FRESHNESS],
                  value=_val(OBS_SRC, Provenance.OBSERVED), state_key="state.verified")
        ]
    )
    assert "STALE_TEMPORAL_EVIDENCE" in _codes(env)


def test_dk_signal_stale_only_asserting_current_is_rejected() -> None:
    env = ResponseEnvelope(
        dk_signals=[
            Claim(statement="the current signal", evidence=[STALE_STATE],
                  value=_dk_val(), state_key="state.supported")
        ]
    )
    assert "STALE_TEMPORAL_EVIDENCE" in _codes(env)


def test_temporal_section_with_a_current_source_is_accepted() -> None:
    # Mixed stale + current: a current source is present ⇒ NOT rejected.
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="the current offer", evidence=[STALE_FRESHNESS, FRESH],
                  value=_val(OBS_SRC, Provenance.OBSERVED), state_key="state.verified")
        ]
    )
    assert "STALE_TEMPORAL_EVIDENCE" not in _codes(env)


def test_stale_disclosure_is_exempt_from_temporal_rule() -> None:
    # Honestly disclosing staleness (state.stale) on stale evidence is grounded,
    # not a false current claim — this preserves the data-quality suite semantics.
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="past the freshness window", evidence=[STALE_FRESHNESS],
                  value=_val(OBS_SRC, Provenance.OBSERVED), state_key="state.stale")
        ]
    )
    assert "STALE_TEMPORAL_EVIDENCE" not in _codes(env)


def test_non_temporal_section_is_exempt_from_freshness() -> None:
    # seller_config is not current-state; stale config evidence is not a temporal
    # violation (a floor set long ago is still the floor).
    env = ResponseEnvelope(
        seller_config=[
            Claim(statement="the floor", evidence=[STALE_FRESHNESS],
                  value=_val(CFG_SRC, Provenance.SELLER_CONFIG))
        ]
    )
    assert "STALE_TEMPORAL_EVIDENCE" not in _codes(env)


# --- 4. POSITIVE: a valid mixed-source section for each type passes ----------


def test_valid_mixed_source_sections_pass() -> None:
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="lowest qualifying offer", evidence=[FRESH],
                  value=_val(OBS_SRC, Provenance.OBSERVED), state_key="state.verified")
        ],
        dk_signals=[
            Claim(statement="seller count", evidence=[CURRENT_VERIFIED],
                  value=_dk_val(), state_key="state.supported")
        ],
        seller_config=[
            Claim(statement="floor configured", evidence=[STALE_FRESHNESS],
                  value=_val(CFG_SRC, Provenance.SELLER_CONFIG), state_key="readiness.complete")
        ],
        deterministic_calculations=[
            Calculation(label="contribution", evidence=[FRESH],
                        result=_val(MARGIN_SRC, Provenance.MARGIN_ENGINE))
        ],
        model_inference="A clean grounded note.",
    )
    codes = _codes(env)
    assert "WRONG_EVIDENCE_CATEGORY" not in codes
    assert "INSUFFICIENT_SOURCES" not in codes
    assert "STALE_TEMPORAL_EVIDENCE" not in codes


# --- 5. SEAM: the composer fails closed carrying the new codes ---------------


def test_composer_fails_closed_on_section_policy_violation() -> None:
    result = compose_or_refuse(
        catalog=UNSCOPED,
        observed_facts=[
            Claim(statement="the current offer", evidence=[STALE_FRESHNESS],
                  value=_val(OBS_SRC, Provenance.OBSERVED), state_key="state.verified")
        ],
    )
    assert isinstance(result, CannotAnswer)
    assert "STALE_TEMPORAL_EVIDENCE" in result.violations
    # Fail-closed refusal carries CODES only — never free-text detail, never digits.
    assert not any(ch.isdigit() for ch in result.message)


def test_composer_fails_closed_on_wrong_category() -> None:
    result = compose_or_refuse(
        catalog=UNSCOPED,
        dk_signals=[
            Claim(statement="a signal", evidence=[FRESH], value=_dk_val(Provenance.OBSERVED))
        ],
    )
    assert isinstance(result, CannotAnswer)
    assert "WRONG_EVIDENCE_CATEGORY" in result.violations

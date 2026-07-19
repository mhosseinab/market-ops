"""Section-scoped provenance: a valid ref from the WRONG section is rejected.

Issue #51 (§12.2 provenance): the grounding walker must prove that every cited
evidence_id and every SourceRef belongs to the set explicitly made available for
that specific section and required evidence category. A globally-valid ref that
was legitimately returned for another section can otherwise be re-attached to
make unsupported content look grounded. The negative (containment) test comes
first: a wrong-section ref is REJECTED (new violation code; ``compose_or_refuse``
⇒ :class:`CannotAnswer`). Then correctly-scoped refs pass and render unchanged.
"""

from __future__ import annotations

import pytest
from llm.envelope.composer import compose, compose_or_refuse
from llm.envelope.contract import (
    AvailabilityCatalog,
    Calculation,
    CannotAnswer,
    Claim,
    Comparison,
    ExposureTotal,
    Provenance,
    ResponseEnvelope,
    SectionScope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.grounding import GroundingError, find_violations, validate_grounding
from llm.envelope.models import EvidenceRef, Money

# --- reusable building blocks ------------------------------------------------

OBS_SRC = SourceRef(tool="read_observation", response_field="offer.price")
DK_SRC = SourceRef(tool="read_observation", response_field="offer.seller_count")
MARGIN_SRC = SourceRef(tool="read_contribution", response_field="contribution.total")

OBS_EVIDENCE = EvidenceRef(
    evidence_id="ev-obs-1", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
)
DK_EVIDENCE = EvidenceRef(
    evidence_id="ev-dk-1", captured_at="2026-07-17T10:00:00Z", quality="state.supported"
)


def _obs_money() -> SourcedValue:
    return SourcedValue(
        source=OBS_SRC,
        provenance=Provenance.OBSERVED,
        money=Money(mantissa=990000, currency="IRR"),
    )


def _dk_count() -> SourcedValue:
    return SourcedValue(source=DK_SRC, provenance=Provenance.DK_SIGNAL, count=4)


def _margin_money() -> SourcedValue:
    return SourcedValue(
        source=MARGIN_SRC,
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=120000, currency="IRR"),
    )


def _codes(env: ResponseEnvelope, catalog: AvailabilityCatalog) -> set[str]:
    return {v.code for v in find_violations(env, catalog=catalog)}


# A catalog built from VALIDATED tool outputs: the observed_facts section was
# given the observed price ref/evidence; the dk_signals section the seller-count
# ref/evidence; the calc section the margin ref. Nothing crosses over.
def _catalog() -> AvailabilityCatalog:
    return AvailabilityCatalog(
        observed_facts=SectionScope(evidence_ids=["ev-obs-1"], sources=[OBS_SRC]),
        dk_signals=SectionScope(evidence_ids=["ev-dk-1"], sources=[DK_SRC]),
        seller_config=SectionScope(evidence_ids=["ev-obs-1"]),
        deterministic_calculations=SectionScope(
            evidence_ids=["ev-obs-1"], sources=[MARGIN_SRC]
        ),
        comparisons=SectionScope(sources=[MARGIN_SRC]),
        exposure=SectionScope(sources=[MARGIN_SRC]),
    )


# --- 1. NEGATIVE: a wrong-section SourceRef is rejected (containment first) ---


def test_source_from_another_section_is_rejected() -> None:
    # DK_SRC is globally valid (it is the dk_signals section's ref) but it is NOT
    # in the observed_facts scope. Attaching it to an observed_fact claim must be
    # rejected, even though the same ref would pass under dk_signals.
    env = ResponseEnvelope(
        observed_facts=[
            Claim(
                statement="lowest qualifying offer",
                evidence=[OBS_EVIDENCE],
                value=SourcedValue(
                    source=DK_SRC,  # wrong section
                    provenance=Provenance.OBSERVED,
                    money=Money(mantissa=990000, currency="IRR"),
                ),
            )
        ],
    )
    assert "SOURCE_OUT_OF_SECTION" in _codes(env, _catalog())
    with pytest.raises(GroundingError):
        validate_grounding(env, catalog=_catalog())


def test_source_from_another_section_fails_closed_via_composer() -> None:
    result = compose_or_refuse(
        catalog=_catalog(),
        observed_facts=[
            Claim(
                statement="lowest qualifying offer",
                evidence=[OBS_EVIDENCE],
                value=SourcedValue(
                    source=DK_SRC,  # wrong section
                    provenance=Provenance.OBSERVED,
                    money=Money(mantissa=990000, currency="IRR"),
                ),
            )
        ],
    )
    assert isinstance(result, CannotAnswer)
    assert "SOURCE_OUT_OF_SECTION" in result.violations


# --- 2. NEGATIVE: a wrong-section evidence_id is rejected --------------------


def test_evidence_from_another_section_is_rejected() -> None:
    # ev-dk-1 is globally valid but only scoped to dk_signals; citing it under an
    # observed_fact claim is unscoped evidence.
    borrowed = EvidenceRef(
        evidence_id="ev-dk-1", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
    )
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="an offer", evidence=[borrowed], value=_obs_money())
        ],
    )
    assert "UNSCOPED_EVIDENCE" in _codes(env, _catalog())


# --- 3. POSITIVE: correctly-scoped refs pass and render unchanged ------------


def test_correctly_scoped_envelope_passes_unchanged() -> None:
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="lowest qualifying offer", evidence=[OBS_EVIDENCE],
                  value=_obs_money())
        ],
        dk_signals=[
            Claim(statement="seller count", evidence=[DK_EVIDENCE], value=_dk_count(),
                  state_key="state.supported")
        ],
        deterministic_calculations=[
            Calculation(label="contribution", result=_margin_money(),
                        evidence=[OBS_EVIDENCE])
        ],
        comparisons=[
            Comparison(label="price vs last read", left=_margin_money(),
                       right=_margin_money(), delta=_margin_money(),
                       left_captured_at="2026-07-16T10:00:00Z",
                       right_captured_at="2026-07-17T10:00:00Z")
        ],
        exposure=ExposureTotal(known=True, total=_margin_money()),
    )
    # No scope violations, and validate_grounding does not raise.
    assert _codes(env, _catalog()) == set()
    validate_grounding(env, catalog=_catalog())
    # The composer returns the SAME envelope content, not a refusal.
    result = compose_or_refuse(
        catalog=_catalog(),
        observed_facts=env.observed_facts,
        dk_signals=env.dk_signals,
        deterministic_calculations=env.deterministic_calculations,
        comparisons=env.comparisons,
        exposure=env.exposure,
    )
    assert isinstance(result, ResponseEnvelope)
    assert result.observed_facts == env.observed_facts


# --- 4. NEGATIVE: a calculation source from another section is rejected ------


def test_calculation_source_from_another_section_is_rejected() -> None:
    env = ResponseEnvelope(
        deterministic_calculations=[
            Calculation(
                label="contribution",
                result=SourcedValue(
                    source=OBS_SRC,  # observed ref, not the margin ref the calc scope allows
                    provenance=Provenance.MARGIN_ENGINE,
                    money=Money(mantissa=120000, currency="IRR"),
                ),
                evidence=[OBS_EVIDENCE],
            )
        ],
    )
    assert "SOURCE_OUT_OF_SECTION" in _codes(env, _catalog())


# --- 5. NEGATIVE: a comparison operand source from another section rejected --


def test_comparison_source_from_another_section_is_rejected() -> None:
    env = ResponseEnvelope(
        comparisons=[
            Comparison(
                label="price move",
                left=_margin_money(),
                right=_margin_money(),
                delta=SourcedValue(
                    source=DK_SRC,  # wrong section
                    provenance=Provenance.MARGIN_ENGINE,
                    money=Money(mantissa=1000, currency="IRR"),
                ),
                left_captured_at="2026-07-16T10:00:00Z",
                right_captured_at="2026-07-17T10:00:00Z",
            )
        ],
    )
    assert "SOURCE_OUT_OF_SECTION" in _codes(env, _catalog())


# --- 6. NEGATIVE: an exposure total source from another section rejected -----


def test_exposure_source_from_another_section_is_rejected() -> None:
    env = ResponseEnvelope(
        exposure=ExposureTotal(
            known=True,
            total=SourcedValue(
                source=OBS_SRC,  # not the margin ref the exposure scope allows
                provenance=Provenance.MARGIN_ENGINE,
                money=Money(mantissa=500000, currency="IRR"),
            ),
        ),
    )
    assert "SOURCE_OUT_OF_SECTION" in _codes(env, _catalog())


# --- 7. SEAM: catalog absent preserves existing behavior --------------------


def test_no_catalog_preserves_existing_behavior() -> None:
    # Without a catalog the section-scope check does not run (existing callers /
    # trusted authored inputs) — but every OTHER grounding rule still applies.
    env = ResponseEnvelope(
        observed_facts=[
            Claim(statement="an offer", evidence=[OBS_EVIDENCE],
                  value=SourcedValue(source=DK_SRC, provenance=Provenance.OBSERVED,
                                     money=Money(mantissa=1, currency="IRR")))
        ],
    )
    codes = {v.code for v in find_violations(env)}
    assert "SOURCE_OUT_OF_SECTION" not in codes
    assert "UNSCOPED_EVIDENCE" not in codes


def test_compose_with_catalog_raises_on_wrong_section() -> None:
    with pytest.raises(GroundingError):
        compose(
            catalog=_catalog(),
            dk_signals=[
                Claim(statement="seller count", evidence=[DK_EVIDENCE],
                      value=SourcedValue(source=MARGIN_SRC,  # wrong section
                                         provenance=Provenance.DK_SIGNAL, count=4))
            ],
        )

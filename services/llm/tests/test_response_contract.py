"""Composer + fail-closed behavior (§12.2, CHAT-002/004/005, §12.4).

The composer places model text ONLY in the inference slot, validates grounding,
and — when the envelope is not grounded — fails closed to a structured
``CannotAnswer`` with a deep link rather than emitting a plausible guess. Money
in a sourced value is never a float.
"""

from __future__ import annotations

import pytest
from llm.envelope.composer import (
    FALLBACK_DEEP_LINK,
    compose,
    compose_or_refuse,
    fail_closed,
)
from llm.envelope.contract import (
    CannotAnswer,
    Claim,
    Provenance,
    ResponseEnvelope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.grounding import GroundingError
from llm.envelope.models import EvidenceRef, Money
from pydantic import ValidationError

GOOD_EVIDENCE = EvidenceRef(
    evidence_id="ev-1", captured_at="2026-07-17T10:00:00Z", quality="state.verified"
)


def _sourced_money() -> SourcedValue:
    return SourcedValue(
        source=SourceRef(tool="read_contribution", response_field="contribution.total"),
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=990000, currency="IRR"),
    )


# --- composer: model text lands only in the inference slot ------------------


def test_compose_places_model_text_only_in_inference() -> None:
    env = compose(
        model_inference="The offer sits just above your floor.",
        observed_facts=[Claim(statement="lowest qualifying offer", evidence=[GOOD_EVIDENCE],
                              value=_sourced_money())],
    )
    assert isinstance(env, ResponseEnvelope)
    assert env.model_inference == "The offer sits just above your floor."
    # The model text never appears in any category-separated field.
    assert env.observed_facts[0].statement != env.model_inference


def test_compose_raises_on_ungrounded_envelope() -> None:
    with pytest.raises(GroundingError):
        compose(observed_facts=[Claim(statement="no evidence claim", evidence=[])])


# --- fail closed: missing evidence ⇒ structured refusal with deep link ------


def test_compose_or_refuse_fails_closed_on_missing_evidence() -> None:
    result = compose_or_refuse(
        observed_facts=[Claim(statement="unsupported claim", evidence=[])],
        missing_data=["evidence for the claim"],
    )
    assert isinstance(result, CannotAnswer)
    assert result.deep_link == FALLBACK_DEEP_LINK
    assert result.deep_link  # names the structured screen
    assert "MISSING_EVIDENCE" in result.violations
    assert result.reason_key == "state.degraded.body"  # canonical catalog key
    # A refusal carries no digits / no fabricated numbers.
    assert not any(ch.isdigit() for ch in result.message)


def test_compose_or_refuse_fails_closed_on_fabricated_number() -> None:
    fabricated = SourcedValue(
        source=SourceRef(tool="", response_field=""),
        provenance=Provenance.MARGIN_ENGINE,
        money=Money(mantissa=123456, currency="IRR"),
    )
    result = compose_or_refuse(
        observed_facts=[Claim(statement="made up", evidence=[GOOD_EVIDENCE], value=fabricated)]
    )
    assert isinstance(result, CannotAnswer)
    assert "FABRICATED_NUMBER" in result.violations


def test_compose_or_refuse_returns_envelope_when_grounded() -> None:
    result = compose_or_refuse(
        model_inference="A short natural-language note.",
        observed_facts=[Claim(statement="ok", evidence=[GOOD_EVIDENCE], value=_sourced_money())],
    )
    assert isinstance(result, ResponseEnvelope)


def test_compose_or_refuse_fails_closed_on_validation_error(monkeypatch) -> None:  # type: ignore[no-untyped-def]
    """A pydantic ValidationError during compose fails closed, not propagates."""
    import llm.envelope.composer as composer_mod

    def _boom(**_kwargs: object) -> ResponseEnvelope:
        Money(mantissa=1.5, currency="IRR")  # type: ignore[arg-type]  # raises ValidationError
        raise AssertionError("unreachable")

    monkeypatch.setattr(composer_mod, "compose", _boom)
    result = composer_mod.compose_or_refuse(model_inference="note")
    assert isinstance(result, CannotAnswer)
    assert "ENVELOPE_MALFORMED" in result.violations
    assert result.deep_link == FALLBACK_DEEP_LINK


def test_fail_closed_helper_shape() -> None:
    refusal = fail_closed(message="cannot answer", missing=["cost"])
    assert refusal.code == "CANNOT_ANSWER"
    assert refusal.deep_link == FALLBACK_DEEP_LINK
    assert refusal.missing == ["cost"]


# --- money is never a float inside a sourced value (§9.1) -------------------


def test_sourced_count_rejects_float() -> None:
    with pytest.raises(ValidationError):
        SourcedValue(
            source=SourceRef(tool="read_observation", response_field="offer.seller_count"),
            provenance=Provenance.DK_SIGNAL,
            count=4.5,  # type: ignore[arg-type]
        )


def test_sourced_count_rejects_bool() -> None:
    with pytest.raises(ValidationError):
        SourcedValue(
            source=SourceRef(tool="read_observation", response_field="offer.seller_count"),
            provenance=Provenance.DK_SIGNAL,
            count=True,
        )


def test_sourced_value_requires_exactly_one_payload() -> None:
    src = SourceRef(tool="t", response_field="f")
    with pytest.raises(ValidationError):
        SourcedValue(source=src, provenance=Provenance.OBSERVED)  # none set
    with pytest.raises(ValidationError):
        SourcedValue(source=src, provenance=Provenance.OBSERVED,
                     money=Money(mantissa=1, currency="IRR"), count=2)  # two set

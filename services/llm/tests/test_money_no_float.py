"""Money invariant inside the LLM plane (§9.1): never float."""

from __future__ import annotations

import pytest
from llm.envelope.models import AssistantAnswer, Money, RawEvidenceValue
from pydantic import ValidationError


def test_money_is_integer_only() -> None:
    m = Money(mantissa=125000, currency="IRR", exponent=-2)
    assert m.mantissa == 125000
    assert m.exponent == -2
    assert m.currency == "IRR"


def test_money_rejects_float_mantissa() -> None:
    with pytest.raises(ValidationError):
        Money(mantissa=1250.0, currency="IRR")  # type: ignore[arg-type]


def test_money_rejects_float_exponent() -> None:
    with pytest.raises(ValidationError):
        Money(mantissa=1250, currency="IRR", exponent=-2.5)  # type: ignore[arg-type]


def test_money_rejects_bool() -> None:
    with pytest.raises(ValidationError):
        Money(mantissa=True, currency="IRR")


def test_money_rejects_bad_currency() -> None:
    with pytest.raises(ValidationError):
        Money(mantissa=1, currency="rial")


def test_assistant_answer_has_no_float_money_field() -> None:
    """The response_format model carries money only as Money / raw strings."""
    answer = AssistantAnswer(
        summary="lowest qualifying offer",
        amounts=[Money(mantissa=990000, currency="IRR", exponent=0)],
        raw_values=[RawEvidenceValue(raw="۹۹۰٬۰۰۰ تومان", unit="toman")],
    )
    dumped = answer.model_dump()
    for amount in dumped["amounts"]:
        assert isinstance(amount["mantissa"], int)
        assert not isinstance(amount["mantissa"], bool)
        assert isinstance(amount["exponent"], int)

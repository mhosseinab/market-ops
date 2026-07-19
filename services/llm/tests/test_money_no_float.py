"""Money invariant inside the LLM plane (§9.1): never float; §15.1: one wire shape.

The envelope :class:`Money` mirrors the gateway ``MoneyAmount`` contract (#73):
``mantissa`` is an exact int64 held internally, but serialized to the SSE / JSON
wire as a signed base-10 decimal STRING (``^-?[0-9]+$``). This keeps Money exact
across the runtime boundary — a JS ``JSON.parse`` of the ``final`` frame sees a
string, never a lossy JS number (>2^53 rounds otherwise).
"""

from __future__ import annotations

import json
import re

import pytest
from llm.envelope.models import (
    AssistantAnswer,
    ChatStreamEvent,
    Money,
    RawEvidenceValue,
    StreamEventKind,
)
from llm.orchestrator.graph import envelope_from_structured
from pydantic import ValidationError

_MANTISSA_WIRE = re.compile(r"^-?[0-9]+$")

# int64 boundaries (mirrors the Go core Money{mantissa int64}).
_INT64_MAX = 2**63 - 1
_INT64_MIN = -(2**63)
_ABOVE_2_53 = 2**53 + 1  # 9007199254740993 — first int a JS number cannot hold


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
    """The response_format model carries money only as Money / raw strings.

    The no-float invariant: ``mantissa`` never round-trips through a float, and
    the JSON-mode wire form is a signed-decimal STRING (§9.1 / #73), while
    ``exponent`` stays a small integer.
    """
    answer = AssistantAnswer(
        summary="lowest qualifying offer",
        amounts=[Money(mantissa=990000, currency="IRR", exponent=0)],
        raw_values=[RawEvidenceValue(raw="۹۹۰٬۰۰۰ تومان", unit="toman")],
    )
    # Python-mode dump keeps the exact int internally (no float, no bool).
    py_dumped = answer.model_dump()
    for amount in py_dumped["amounts"]:
        assert isinstance(amount["mantissa"], int)
        assert not isinstance(amount["mantissa"], bool)
        assert isinstance(amount["exponent"], int)
    # JSON-mode (wire) dump encodes mantissa as a signed-decimal string.
    json_dumped = answer.model_dump(mode="json")
    for amount in json_dumped["amounts"]:
        assert isinstance(amount["mantissa"], str)
        assert _MANTISSA_WIRE.match(amount["mantissa"])
        assert isinstance(amount["exponent"], int)


def test_money_serializes_mantissa_as_signed_decimal_string() -> None:
    """JSON wire form of mantissa is a string matching ^-?[0-9]+$ (§9.1 / #73)."""
    m = Money(mantissa=990000, currency="IRR", exponent=-2)
    payload = json.loads(m.model_dump_json())
    assert isinstance(payload["mantissa"], str)
    assert payload["mantissa"] == "990000"
    assert _MANTISSA_WIRE.match(payload["mantissa"])
    # exponent remains a JSON number (small int8, no precision hazard).
    assert isinstance(payload["exponent"], int)
    assert payload["exponent"] == -2


@pytest.mark.parametrize(
    "mantissa",
    [
        _ABOVE_2_53,  # 9007199254740993 — the classic JS-number rounding case
        _INT64_MAX,  # 9223372036854775807
        _INT64_MIN,  # -9223372036854775808
        -_ABOVE_2_53,
        0,
    ],
)
def test_money_mantissa_round_trips_without_precision_loss(mantissa: int) -> None:
    """Large / boundary int64 mantissas survive the JSON wire exactly.

    A JS ``JSON.parse`` of this frame sees a string, so parsing to BigInt is
    exact — no JS-number intermediate (the #73 defect).
    """
    m = Money(mantissa=mantissa, currency="IRR", exponent=0)
    wire = m.model_dump_json()
    payload = json.loads(wire)
    # The wire field is a string (what JS.parse observes), not a number.
    assert isinstance(payload["mantissa"], str)
    assert payload["mantissa"] == str(mantissa)
    # Re-parsing the string yields the exact integer — round-trip is lossless.
    assert int(payload["mantissa"]) == mantissa
    # Model re-validation from the string wire form reconstructs the exact int.
    reparsed = Money.model_validate(payload)
    assert reparsed.mantissa == mantissa


def test_money_accepts_string_wire_form_on_input() -> None:
    """The string wire form is accepted and stored as the exact integer."""
    m = Money.model_validate({"mantissa": "9007199254740993", "currency": "IRR"})
    assert m.mantissa == _ABOVE_2_53
    assert m.exponent == 0


def test_money_rejects_non_decimal_mantissa_string() -> None:
    """A non-``^-?[0-9]+$`` mantissa string fails closed — never coerced (§9.1)."""
    for bad in ["1.5", "1e3", "0x10", "  12", "12 ", "", "-", "+12", "abc", "۱۲۳"]:
        with pytest.raises(ValidationError):
            Money.model_validate({"mantissa": bad, "currency": "IRR"})


def test_money_rejects_out_of_int64_range_mantissa() -> None:
    """A mantissa beyond signed int64 fails closed (matches gateway decode)."""
    for bad in [_INT64_MAX + 1, _INT64_MIN - 1, str(_INT64_MAX + 1), str(_INT64_MIN - 1)]:
        with pytest.raises(ValidationError):
            Money.model_validate({"mantissa": bad, "currency": "IRR"})


@pytest.mark.parametrize(
    "mantissa",
    [
        _ABOVE_2_53,  # 9007199254740993 — the classic JS-number rounding case
        _INT64_MAX,  # 9223372036854775807
        _INT64_MIN,  # -9223372036854775808
        -_ABOVE_2_53,
        0,
    ],
)
def test_chat_stream_final_frame_carries_string_mantissa(mantissa: int) -> None:
    """The SSE ``final`` frame delivered to web has a string mantissa.

    This exercises the REAL production seam, not a hand-applied JSON-mode dump:
    :func:`envelope_from_structured` is exactly what ``graph.py`` uses to build
    the ``answer`` dict, and ``app.py`` wraps that dict verbatim in a
    ``ChatStreamEvent(kind=FINAL, envelope=...)`` and calls ``to_sse``. If the
    helper dumped in Python mode (as production did before #73's real fix), the
    int ``mantissa`` re-serializes here as a JSON NUMBER — the #73 defect web
    ``JSON.parse`` rounds. The assertion below tracks production and fails closed
    on any non-string / non-decimal mantissa.
    """
    structured = AssistantAnswer(
        summary="lowest qualifying offer",
        amounts=[Money(mantissa=mantissa, currency="IRR", exponent=0)],
    )
    # Identical to graph.py: structured -> JSON-safe envelope dict.
    answer = envelope_from_structured(structured)
    # Identical to app.py: wrap the dict verbatim and emit the SSE final frame.
    event = ChatStreamEvent(
        kind=StreamEventKind.FINAL,
        conversation_id="c-1",
        envelope=answer,
    )
    frame = event.to_sse()
    assert frame.startswith("data: ")
    assert frame.endswith("\n\n")
    payload = json.loads(frame[len("data: ") : -2])
    wire_mantissa = payload["envelope"]["amounts"][0]["mantissa"]
    # What web's JSON.parse observes on this exact frame: a string, never a
    # (lossy) JSON number, and never a float/bool.
    assert isinstance(wire_mantissa, str)
    assert not isinstance(wire_mantissa, bool)
    assert _MANTISSA_WIRE.fullmatch(wire_mantissa)
    assert wire_mantissa == str(mantissa)
    # Round-trips back to the exact int64 with no precision loss.
    assert int(wire_mantissa) == mantissa

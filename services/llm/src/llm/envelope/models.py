"""Pydantic models for the LLM plane's typed outputs and SSE frames.

These are the ``response_format`` and stream shapes. The money invariant (§9.1)
is enforced structurally: :class:`Money` has only integer/string fields and a
validator rejecting anything a float could sneak through. A model authored to
carry a price uses :class:`Money` or a :class:`RawEvidenceValue` — never a bare
number — so the model can never emit an authoritative float.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Annotated, Any

from pydantic import BaseModel, ConfigDict, Field, StringConstraints, field_validator

# ISO-4217 alpha-3 currency code (LTR technical identifier).
CurrencyCode = Annotated[str, StringConstraints(min_length=3, max_length=3, pattern=r"^[A-Z]{3}$")]


class Money(BaseModel):
    """A money amount, integer-only (PRD §9.1 — no float on any money path).

    ``value == mantissa * 10**exponent`` in ``currency``. This mirrors the Go
    core's ``Money{mantissa int64, currency, exponent int8}``; the LLM plane
    never computes an authoritative amount — it only relays engine outputs in
    this shape. Floats are structurally impossible: both numeric fields are
    ``int`` and the validator rejects any non-integer input.
    """

    model_config = ConfigDict(extra="forbid", strict=True)

    mantissa: int
    currency: CurrencyCode
    exponent: int = 0

    @field_validator("mantissa", "exponent", mode="before")
    @classmethod
    def _reject_float(cls, v: Any) -> Any:
        # bool is an int subclass but never a money component; reject it too.
        if isinstance(v, bool):
            raise ValueError("money components must be integers, not bool")
        if isinstance(v, float):
            raise ValueError("money components must be integers, never float (§9.1)")
        return v


class RawEvidenceValue(BaseModel):
    """Raw marketplace text/value/unit preserved as evidence, separate from Money.

    When a source unit is ambiguous the value is quarantined here, never inferred
    into a Money (§9.1). The string is preserved verbatim; no arithmetic.
    """

    model_config = ConfigDict(extra="forbid")

    raw: str
    unit: str | None = None


class EvidenceRef(BaseModel):
    """A reference to structured evidence backing a claim (§12.2/CHAT-005)."""

    model_config = ConfigDict(extra="forbid")

    evidence_id: str
    captured_at: str  # RFC 3339 UTC; a historical time never reads as current.
    quality: str  # canonical quality state (glossary); relayed, not invented.


class AssistantAnswer(BaseModel):
    """The agent's typed structured output (``response_format``).

    S20 ships the money-safe, category-aware skeleton the model fills only in its
    natural-language slots; the full §12.2 category separation and grounding
    validation land in S22. Any monetary figure lives in :class:`Money` /
    :class:`RawEvidenceValue`, never a float, so the model cannot restate a price
    as a number.
    """

    model_config = ConfigDict(extra="forbid")

    # Natural-language slot the model fills. Carries no authority.
    summary: str
    # Structured evidence the answer references (may be empty in S20).
    evidence: list[EvidenceRef] = Field(default_factory=list)
    # Any monetary figures, in engine-output form only.
    amounts: list[Money] = Field(default_factory=list)
    # Raw, possibly-quarantined values kept as evidence strings.
    raw_values: list[RawEvidenceValue] = Field(default_factory=list)
    # Fields whose data is missing/unknown — rendered as unknown, never guessed.
    missing_data: list[str] = Field(default_factory=list)


class TurnFailure(BaseModel):
    """The §12.4 structured failure state.

    Emitted after the single automatic retry is exhausted, or when a hard bound
    (graph recursion, tool-call limit, per-tool timeout, token ceiling) trips.
    Free text only; carries no authority. Always names a deep link to the
    structured screen that completes the task deterministically.
    """

    model_config = ConfigDict(extra="forbid")

    code: str
    message: str
    deep_link: str | None = None


class StreamEventKind(StrEnum):
    """SSE frame discriminator (mirrors the gateway ChatStreamEvent contract)."""

    CONVERSATION = "conversation"
    TOKEN = "token"
    FINAL = "final"
    FAILURE = "failure"


class ChatStreamEvent(BaseModel):
    """One SSE ``data:`` frame emitted by the LLM plane's /chat endpoint.

    Never carries an approval control. ``envelope`` holds the final typed answer
    on the ``final`` frame; ``failure`` holds the §12.4 state on the ``failure``
    frame.
    """

    model_config = ConfigDict(extra="forbid")

    kind: StreamEventKind
    conversation_id: str | None = None
    token: str | None = None
    envelope: dict[str, Any] | None = None
    failure: TurnFailure | None = None

    def to_sse(self) -> str:
        """Serialize as a single SSE frame (``data: <json>\\n\\n``)."""
        return f"data: {self.model_dump_json(exclude_none=True)}\n\n"

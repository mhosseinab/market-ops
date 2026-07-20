"""Pydantic models for the LLM plane's typed outputs and SSE frames.

These are the ``response_format`` and stream shapes. The money invariant (§9.1)
is enforced structurally: :class:`Money` has only integer/string fields and a
validator rejecting anything a float could sneak through. A model authored to
carry a price uses :class:`Money` or a :class:`RawEvidenceValue` — never a bare
number — so the model can never emit an authoritative float.
"""

from __future__ import annotations

import re
from enum import StrEnum
from typing import Annotated, Any

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_serializer,
    field_validator,
)

from llm.flows.deep_links import validate_recovery_route

# ISO-4217 alpha-3 currency code (LTR technical identifier).
CurrencyCode = Annotated[str, StringConstraints(min_length=3, max_length=3, pattern=r"^[A-Z]{3}$")]

# Signed base-10 decimal on the wire — mirrors the gateway MoneyAmount contract
# (#73, §15.1). Anchored, no whitespace, no sign-only, no Latin/Persian noise.
_MANTISSA_WIRE = re.compile(r"^-?[0-9]+$")

# The mantissa is an int64 in the Go core Money; the wire form must fail closed
# outside that range (quarantine over inference — never coerce), matching the
# gateway's strconv decode.
_INT64_MIN = -(2**63)
_INT64_MAX = 2**63 - 1


class Money(BaseModel):
    """A money amount, exact-integer (PRD §9.1 — no float on any money path).

    ``value == mantissa * 10**exponent`` in ``currency``. This mirrors the Go
    core's ``Money{mantissa int64, currency, exponent int8}`` and the gateway
    ``MoneyAmount`` contract; the LLM plane never computes an authoritative
    amount — it only relays engine outputs in this shape.

    ``mantissa`` is held internally as an exact Python ``int`` but serialized to
    the JSON / SSE wire as a signed base-10 decimal STRING (``^-?[0-9]+`` within
    int64 range), identical to the gateway ``MoneyAmount.mantissa`` (#73, §15.1).
    A JS ``JSON.parse`` of the ``final`` frame therefore sees a string, so any
    int64 above 2^53 survives without JS-number precision loss. Floats are
    structurally impossible: the validator rejects any non-integer input and any
    non-decimal / out-of-int64-range string, failing closed rather than coercing.
    """

    model_config = ConfigDict(extra="forbid", strict=True)

    mantissa: int
    currency: CurrencyCode
    exponent: int = 0

    @field_validator("mantissa", mode="before")
    @classmethod
    def _coerce_mantissa(cls, v: Any) -> Any:
        # bool is an int subclass but never a money component; reject it too.
        if isinstance(v, bool):
            raise ValueError("mantissa must be an integer, not bool")
        if isinstance(v, float):
            raise ValueError("mantissa must be an integer, never float (§9.1)")
        if isinstance(v, str):
            # Accept the wire form; fail closed on anything non-decimal.
            if not _MANTISSA_WIRE.fullmatch(v):
                raise ValueError("mantissa string must match ^-?[0-9]+$ (§9.1, #73)")
            v = int(v)
        if not isinstance(v, int):
            raise ValueError("mantissa must be an integer or signed-decimal string")
        if not _INT64_MIN <= v <= _INT64_MAX:
            raise ValueError("mantissa must be within signed int64 range (§9.1)")
        return v

    @field_validator("exponent", mode="before")
    @classmethod
    def _reject_float_exponent(cls, v: Any) -> Any:
        # bool is an int subclass but never a money component; reject it too.
        if isinstance(v, bool):
            raise ValueError("exponent must be an integer, not bool")
        if isinstance(v, float):
            raise ValueError("exponent must be an integer, never float (§9.1)")
        return v

    @field_serializer("mantissa", when_used="json")
    def _serialize_mantissa(self, v: int) -> str:
        # Wire form matches the gateway MoneyAmount contract: signed decimal
        # STRING, so no plane emits a lossy JS-number mantissa (#73, §15.1).
        return str(v)


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
    structured screen that completes the task deterministically — and that link
    is constrained to the closed set of internal recovery routes so a failure
    fallback can never become an open redirect or an unsafe surface (issue #56).
    """

    model_config = ConfigDict(extra="forbid")

    code: str
    message: str
    deep_link: str | None = None

    @field_validator("deep_link")
    @classmethod
    def _validate_recovery_route(cls, v: str | None) -> str | None:
        # A failure deep link is a deterministic recovery route or nothing at
        # all; a model-authored/free-form path fails closed (§12.4, issue #56).
        if v is None:
            return v
        return validate_recovery_route(v)


class StreamEventKind(StrEnum):
    """SSE frame discriminator (mirrors the gateway ChatStreamEvent contract)."""

    CONVERSATION = "conversation"
    TOKEN = "token"
    FINAL = "final"
    FAILURE = "failure"


class ChatStreamEvent(BaseModel):
    """One SSE ``data:`` frame on the /chat stream (shared gateway↔LLM-plane contract).

    Never carries an approval control. ``envelope`` holds the final typed answer
    on the ``final`` frame; ``failure`` holds the §12.4 state on the ``failure``
    frame.

    ``extra="forbid"`` keeps the stream fail-closed: an unrecognized frame kind or
    an unknown field is a ``ValidationError``, never a silently skipped control
    frame (issue #163). The gateway is the sole author of the ``conversation``
    frame's authoritative context echo — it REPLACES the LLM plane's frame with the
    resolved id, the deterministic context binding, and the bound locale, all
    camelCase (services/core/internal/httpapi/chat_context_frame.go, CHAT-007/#115,
    LOC-001/#120). Those keys are modeled here as known-optional aliases so the
    shared contract mirrors the frame the browser (and the S32 replay harness that
    reuses this model) actually sees, while everything the gateway does NOT emit
    still fails closed. The camelCase mapping is a ``validation_alias`` (parse-only,
    not a serialization alias): ``to_sse`` therefore stays snake_case — the LLM plane
    emits ``conversation_id`` and the gateway alone authors the camelCase echo — and
    ``validate_by_name`` keeps the LLM plane constructing frames by field name.
    """

    model_config = ConfigDict(extra="forbid", validate_by_name=True, validate_by_alias=True)

    kind: StreamEventKind
    conversation_id: str | None = Field(default=None, validation_alias="conversationId")
    token: str | None = None
    envelope: dict[str, Any] | None = None
    failure: TurnFailure | None = None
    # Gateway-authored `conversation`-frame context echo — nil on the LLM plane's own
    # frame; stamped by the gateway from the binding/locale it resolved in BeginTurn.
    context_kind: str | None = Field(default=None, validation_alias="contextKind")
    context_entity_id: str | None = Field(default=None, validation_alias="contextEntityId")
    context_version: int | None = Field(default=None, validation_alias="contextVersion")
    locale_tag: str | None = Field(default=None, validation_alias="localeTag")
    locale_version: int | None = Field(default=None, validation_alias="localeVersion")

    def to_sse(self) -> str:
        """Serialize as a single SSE frame (``data: <json>\\n\\n``)."""
        return f"data: {self.model_dump_json(exclude_none=True)}\n\n"

"""The §12.2 response envelope: category-separated, evidence-bound, money-safe.

Every operational response is a :class:`ResponseEnvelope`. It keeps the seven
§12.2 statement kinds in *distinct* fields — observed fact, DK-provided signal,
seller configuration, deterministic calculation, model inference, missing data,
recommendation — so the surfaces (and the grounding validator in
:mod:`llm.envelope.grounding`) can treat each kind by its own rules:

* every numeric financial value is carried in a :class:`SourcedValue` that names
  the typed service response field it was *copied* from (CHAT-002) — a bare
  number never appears in a numeric slot, and never in the model-text slot;
* money is :class:`Money` (integer mantissa/currency/exponent) or a raw evidence
  string, NEVER a float (§9.1), reusing the S20 primitives;
* operational claims carry :class:`EvidenceRef`s (id + capture time + quality)
  so age and quality travel with the claim (CHAT-005);
* the model fills ONLY :attr:`ResponseEnvelope.model_inference` — every other
  field is composed from typed data by the composer, never authored by the model.

This module only *shapes* the envelope. The rules that REJECT a bad envelope
(fabricated number, missing evidence, oversized table, non-canonical state term,
comparison missing a side, exposure not from the margin engine) live in the
walking validator so the enforcement point is explicit and testable.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Any, Final

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from llm.envelope.models import EvidenceRef, Money, RawEvidenceValue

# Inline tables stop at this many rows; beyond it the response summarizes and
# deep-links instead of dumping rows into chat (CHAT-023).
MAX_INLINE_ROWS = 20


class Provenance(StrEnum):
    """Where a value originated — the §12.2 category axis for a numeric value.

    A value's provenance decides which rules apply: an exposure total is only
    trustworthy from :attr:`MARGIN_ENGINE` (CHAT-012); a deterministic
    calculation must come from an engine, never from observation or the model.
    """

    OBSERVED = "observed"  # observed marketplace fact
    DK_SIGNAL = "dk_signal"  # DK-provided signal (buy-box, seller count, ...)
    SELLER_CONFIG = "seller_config"  # the seller's own configuration
    MARGIN_ENGINE = "margin_engine"  # go_domain_executor margin engine output
    PRICING_ENGINE = "pricing_engine"  # go_domain_executor pricing/policy output


ENGINE_PROVENANCES = frozenset({Provenance.MARGIN_ENGINE, Provenance.PRICING_ENGINE})


class ComparisonKind(StrEnum):
    """What two operands of a comparison represent — the entity-binding axis.

    A comparison is either a *temporal* before/after reading of the SAME entity
    at two capture times, or a *cross-entity* A/B comparison of two DISTINCT
    entities. The kind fixes what a coherent entity binding looks like: the
    grounding walker rejects a temporal comparison whose two operands name
    different entities, and a cross-entity comparison whose two operands name the
    same entity (issue #55). This is structural provenance, not model guesswork.
    """

    TEMPORAL = "temporal"  # same entity, two capture times (before/after)
    CROSS_ENTITY = "cross_entity"  # two distinct entities (A vs B)


class ComparisonRelation(StrEnum):
    """The claimed direction of a comparison, checked against the signed delta.

    The relation describes how the RIGHT operand relates to the LEFT (baseline)
    operand, i.e. the sign of ``left − right``:

    * :attr:`DECREASE` — ``right < left`` (``left − right > 0``);
    * :attr:`INCREASE` — ``right > left`` (``left − right < 0``);
    * :attr:`UNCHANGED` — ``right == left`` (``left − right == 0``).

    The grounding walker (issue #55) recomputes the signed relation from the
    typed operand mantissas/counts and rejects a claimed relation that does not
    agree — a model can never assert a direction the numbers don't support.
    """

    INCREASE = "increase"
    DECREASE = "decrease"
    UNCHANGED = "unchanged"


class SourceRef(BaseModel):
    """A reference to the typed service response field a value was copied from.

    ``tool`` is the registry read-tool that returned the value; ``response_field``
    is the dotted path into that tool's typed response (e.g.
    ``"contribution.total"``). Both must be non-empty — the grounding walker
    rejects an empty reference as a fabricated number (CHAT-002). They are kept
    permissive at construction so the *validator*, not the type, is the single
    enforcement point.
    """

    model_config = ConfigDict(extra="forbid")

    tool: str
    response_field: str

    def is_present(self) -> bool:
        """True only when both the tool and the field path are non-empty."""
        return bool(self.tool.strip()) and bool(self.response_field.strip())


class SectionScope(BaseModel):
    """The evidence IDs and SourceRefs legitimately available for ONE section.

    Built from validated tool outputs, this is the authoritative allow-set for a
    single response section and its required evidence category (issue #51 / §12.2
    provenance). ``evidence_ids`` are the ``EvidenceRef.evidence_id`` values the
    tools returned for this section; ``sources`` are the ``(tool, response_field)``
    references those tools exposed for this section. A claim/value that cites an
    evidence_id or SourceRef absent here — even one globally valid in ANOTHER
    section — is rejected by the grounding walker. Both fields are JSON-safe lists
    so the whole catalog can live in graph state.
    """

    model_config = ConfigDict(extra="forbid")

    evidence_ids: list[str] = Field(default_factory=list)
    sources: list[SourceRef] = Field(default_factory=list)

    def allows_evidence(self, evidence_id: str) -> bool:
        """True when ``evidence_id`` was made available for this section."""
        return evidence_id in set(self.evidence_ids)

    def allows_source(self, ref: SourceRef) -> bool:
        """True when the exact ``(tool, response_field)`` is in this section's set."""
        return any(
            s.tool == ref.tool and s.response_field == ref.response_field
            for s in self.sources
        )


class AvailabilityCatalog(BaseModel):
    """Per-section authoritative availability, built from validated tool outputs.

    Each response section maps to the :class:`SectionScope` of evidence IDs and
    SourceRefs explicitly made available for that section and required evidence
    category. The grounding walker uses it to REJECT any claim/section that cites
    an evidence_id or SourceRef not scoped to that section — closing the issue #51
    gap where a valid ref from one section is re-attached to another to make
    unsupported content look grounded. Every field is JSON-safe (lists of
    strings / typed SourceRefs), so the catalog is safe to hold in graph state.
    """

    model_config = ConfigDict(extra="forbid")

    observed_facts: SectionScope = Field(default_factory=SectionScope)
    dk_signals: SectionScope = Field(default_factory=SectionScope)
    seller_config: SectionScope = Field(default_factory=SectionScope)
    deterministic_calculations: SectionScope = Field(default_factory=SectionScope)
    comparisons: SectionScope = Field(default_factory=SectionScope)
    exposure: SectionScope = Field(default_factory=SectionScope)


class _Unscoped:
    """Sentinel: a CONSCIOUS opt-out of section-scope enforcement (issue #51).

    The live compose boundary makes the :class:`AvailabilityCatalog` mandatory so
    that forgetting it fails closed (a ``TypeError`` at the call site) rather than
    silently skipping the section-scope check and reopening the #51 spoof gap. A
    caller that legitimately has no scope data yet — authored / trusted / eval
    inputs that predate an authoritative catalog — must opt out EXPLICITLY by
    passing this singleton (``catalog=UNSCOPED``); it is self-documenting in the
    call site and impossible to reach by omission.

    Downstream: **S23** wires an authoritative :class:`AvailabilityCatalog` built
    from validated tool outputs into the live compose path, at which point these
    conscious opt-outs are replaced by the real per-section availability.
    """

    __slots__ = ()

    def __repr__(self) -> str:  # pragma: no cover - debugging aid only
        return "UNSCOPED"


# The one shared trust-all marker. Pass it EXPLICITLY to skip section scoping;
# passing nothing at the compose boundary is a TypeError (fails closed).
UNSCOPED: Final[_Unscoped] = _Unscoped()

# What the mandatory compose boundary accepts: a real per-section catalog that is
# enforced, or the explicit trust-all sentinel. There is deliberately NO ``None``
# member — omission cannot silently disable scope enforcement.
CatalogArg = AvailabilityCatalog | _Unscoped


class SourcedValue(BaseModel):
    """A single value copied from a typed service response, with its provenance.

    Exactly one of :attr:`money`, :attr:`count`, or :attr:`raw` is set. Money is
    integer-only (§9.1); ``count`` is an integer, non-money numeric (e.g. a seller
    count) and rejects floats/bools structurally; ``raw`` preserves a marketplace
    string (possibly quarantined) verbatim. Whichever it is, :attr:`source` names
    the response field it came from and :attr:`provenance` names the origin — the
    model never mints one of these.
    """

    model_config = ConfigDict(extra="forbid")

    source: SourceRef
    provenance: Provenance
    money: Money | None = None
    count: int | None = None
    raw: RawEvidenceValue | None = None

    @field_validator("count", mode="before")
    @classmethod
    def _count_is_integer(cls, v: Any) -> Any:
        if v is None:
            return v
        if isinstance(v, bool):
            raise ValueError("count must be an integer, not bool")
        if isinstance(v, float):
            raise ValueError("count must be an integer, never float (§9.1 numeric rule)")
        return v

    @model_validator(mode="after")
    def _exactly_one_payload(self) -> SourcedValue:
        set_count = sum(x is not None for x in (self.money, self.count, self.raw))
        if set_count != 1:
            raise ValueError("a SourcedValue carries exactly one of money/count/raw")
        return self

    def is_numeric(self) -> bool:
        """True when this value is an authoritative number (money or a count).

        Raw evidence strings are NOT numerics: they are preserved marketplace
        text (and quarantined ambiguous units), never arithmetic operands.
        """
        return self.money is not None or self.count is not None


class Claim(BaseModel):
    """An operational claim: an observed fact, a DK signal, or seller config.

    Every operational claim carries at least one :class:`EvidenceRef` so its age
    and quality travel with it (CHAT-005); a claim without evidence fails the
    walker and the turn fails closed. ``value`` optionally attaches the typed,
    sourced number the claim is about; ``state_key`` optionally names a state,
    which must be a canonical catalog key (CHAT-022).
    """

    model_config = ConfigDict(extra="forbid")

    statement: str
    evidence: list[EvidenceRef] = Field(default_factory=list)
    value: SourcedValue | None = None
    state_key: str | None = None


class Calculation(BaseModel):
    """A deterministic calculation result, straight from an engine.

    The result is always a :class:`SourcedValue`; the walker requires its
    provenance to be an engine (margin/pricing) — the model never computes an
    authoritative figure (§12.3), it only relays one.
    """

    model_config = ConfigDict(extra="forbid")

    label: str
    result: SourcedValue
    evidence: list[EvidenceRef] = Field(default_factory=list)


class Comparison(BaseModel):
    """A before/after (or A/B) comparison — CHAT-021, issue #55.

    Carries BOTH values, the delta, and BOTH capture timestamps. The walker
    rejects a comparison missing any side, the delta, or either timestamp, so a
    comparison can never imply a trend from a single reading.

    Beyond token presence, the comparison is a STRUCTURAL relation whose whole
    provenance tuple is bound and checkable (issue #55): each operand names the
    entity it belongs to (:attr:`left_entity` / :attr:`right_entity` — technical
    LTR identifiers), the comparison declares its :attr:`kind` (temporal vs
    cross-entity) and its claimed :attr:`relation` (direction). The grounding
    walker re-derives unit/type coherence, the exact integer delta, the
    entity binding, and the signed direction from the typed operands and REJECTS
    any incoherent combination — so individually-sourced numbers can never be
    recombined into a numerically false comparison. Entity identifiers are held
    permissively (empty allowed at construction); the walker is the single
    enforcement point (``COMPARISON_UNBOUND_ENTITY``), mirroring
    :class:`SourceRef`.
    """

    model_config = ConfigDict(extra="forbid")

    label: str
    left: SourcedValue
    right: SourcedValue
    delta: SourcedValue
    left_captured_at: str
    right_captured_at: str
    kind: ComparisonKind
    relation: ComparisonRelation
    left_entity: str
    right_entity: str


class ExposureTotal(BaseModel):
    """A total exposure figure — CHAT-012: only ever from the margin engine.

    When exposure cannot be computed it renders as *unknown*: set ``known=False``
    and leave ``total`` unset. When known, ``total`` must be a margin-engine
    :class:`SourcedValue`; the walker rejects any other provenance so a guessed
    total can never masquerade as authoritative.
    """

    model_config = ConfigDict(extra="forbid")

    known: bool
    total: SourcedValue | None = None

    @model_validator(mode="after")
    def _known_iff_total(self) -> ExposureTotal:
        if self.known and self.total is None:
            raise ValueError("a known exposure must carry its margin-engine total")
        if not self.known and self.total is not None:
            raise ValueError("an unknown exposure renders as unknown — carry no total")
        return self

    @classmethod
    def unknown(cls) -> ExposureTotal:
        """The CHAT-012 unknown state: rendered as unknown, never a guess."""
        return cls(known=False, total=None)


class InlineTable(BaseModel):
    """An inline table — CHAT-023: at most :data:`MAX_INLINE_ROWS` rows.

    ``rows`` holds the inline slice; ``total_row_count`` is how many rows exist
    in total. When more rows exist than are shown, the response must summarize
    (``summary``) and deep-link (``deep_link``) rather than truncate silently —
    the walker enforces both the 20-row cap and the summarize+deep-link rule.
    """

    model_config = ConfigDict(extra="forbid")

    columns: list[str]
    rows: list[list[str]] = Field(default_factory=list)
    total_row_count: int
    summary: str | None = None
    deep_link: str | None = None


class Recommendation(BaseModel):
    """The recommended action — a distinct §12.2 field, never an approval.

    Free text describing what the seller might do, plus a deep link to the
    structured screen that actually performs it. It carries no authority: it
    cannot approve, execute, or confirm (§12.3). A named state must be canonical.
    """

    model_config = ConfigDict(extra="forbid")

    statement: str
    deep_link: str | None = None
    state_key: str | None = None


class ResponseEnvelope(BaseModel):
    """A complete §12.2 operational response with the seven kinds separated.

    The composer fills every field except :attr:`model_inference` from typed
    data; the model fills ONLY :attr:`model_inference`, and even there the walker
    forbids financial numbers (CHAT-002). Validation is delegated to
    :func:`llm.envelope.grounding.validate_grounding`.
    """

    model_config = ConfigDict(extra="forbid")

    observed_facts: list[Claim] = Field(default_factory=list)
    dk_signals: list[Claim] = Field(default_factory=list)
    seller_config: list[Claim] = Field(default_factory=list)
    deterministic_calculations: list[Calculation] = Field(default_factory=list)
    # The ONLY model-authored slot. Natural language, no authority, no numbers.
    model_inference: str = ""
    missing_data: list[str] = Field(default_factory=list)
    recommendation: Recommendation | None = None
    comparisons: list[Comparison] = Field(default_factory=list)
    tables: list[InlineTable] = Field(default_factory=list)
    exposure: ExposureTotal | None = None

    def operational_claims(self) -> list[Claim]:
        """Every claim that must carry evidence (CHAT-005)."""
        return [*self.observed_facts, *self.dk_signals, *self.seller_config]


class CannotAnswer(BaseModel):
    """The fail-closed structured refusal (CHAT-005, §12.4).

    Emitted instead of a plausible-looking guess when required evidence is
    missing or an envelope fails grounding. Always names a deep link to the
    structured screen and the canonical reason key; ``violations`` records the
    grounding codes for audit. Carries no authority and no numbers.
    """

    model_config = ConfigDict(extra="forbid")

    code: str = "CANNOT_ANSWER"
    reason_key: str
    message: str
    deep_link: str
    missing: list[str] = Field(default_factory=list)
    violations: list[str] = Field(default_factory=list)

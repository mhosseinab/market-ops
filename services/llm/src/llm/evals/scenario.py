"""Turn a factual-support fixture into a REAL §12.2 response — or a refusal.

Each factual fixture (pricing / data-quality / boundary / listing) carries the
typed, sourced evidence a real turn would assemble. This module rebuilds those
compact shapes into the actual contract types and runs them through the REAL
:func:`llm.envelope.composer.compose_or_refuse` — the same grounding path the
live turn uses. The harness then compares the resulting disposition
(``supported`` ⇒ a grounded :class:`ResponseEnvelope`; ``fail_closed`` ⇒ a
:class:`CannotAnswer`) against the fixture's ``expected`` field.

The point is that factual support is measured by the ARCHITECTURE, not by trusting
the model: a well-evidenced answer composes; a degraded one fails closed to a
structured refusal — never a plausible-looking guess.
"""

from __future__ import annotations

from typing import Any

from llm.envelope.composer import compose_or_refuse
from llm.envelope.contract import (
    UNSCOPED,
    AvailabilityCatalog,
    Calculation,
    CannotAnswer,
    CatalogArg,
    Claim,
    Comparison,
    ComparisonKind,
    ComparisonRelation,
    ExposureTotal,
    Provenance,
    Recommendation,
    ResponseEnvelope,
    SectionScope,
    SourcedValue,
    SourceRef,
)
from llm.envelope.models import EvidenceRef, Money, RawEvidenceValue
from llm.localization import FALLBACK_LOCALE_TAG


def _sourced_value(spec: dict[str, Any] | None) -> SourcedValue | None:
    if spec is None:
        return None
    source = SourceRef(tool=spec.get("tool", ""), response_field=spec.get("field", ""))
    provenance = Provenance(spec["provenance"])
    money = None
    count = None
    raw = None
    if spec.get("money") is not None:
        m = spec["money"]
        money = Money(mantissa=m["mantissa"], currency=m["currency"], exponent=m.get("exponent", 0))
    elif spec.get("count") is not None:
        count = spec["count"]
    elif spec.get("raw") is not None:
        r = spec["raw"]
        raw = RawEvidenceValue(raw=r["raw"], unit=r.get("unit"))
    return SourcedValue(source=source, provenance=provenance, money=money, count=count, raw=raw)


def _evidence(specs: list[dict[str, str]]) -> list[EvidenceRef]:
    return [
        EvidenceRef(
            evidence_id=e.get("evidence_id", ""),
            captured_at=e.get("captured_at", ""),
            quality=e.get("quality", ""),
        )
        for e in specs
    ]


def _claim(spec: dict[str, Any]) -> Claim:
    return Claim(
        statement=spec.get("statement", ""),
        evidence=_evidence(spec.get("evidence", [])),
        value=_sourced_value(spec.get("value")),
        state_key=spec.get("state_key"),
    )


def _claims(specs: list[dict[str, Any]]) -> list[Claim]:
    return [_claim(s) for s in specs]


def _calculation(spec: dict[str, Any]) -> Calculation:
    return Calculation(
        label=spec.get("label", ""),
        result=_sourced_value(spec["result"]),  # type: ignore[arg-type]
        evidence=_evidence(spec.get("evidence", [])),
    )


def _comparison(spec: dict[str, Any]) -> Comparison:
    # ``kind`` / ``relation`` are structural (issue #55): a fixture that carries a
    # comparison must declare both so the walker can bind the entity kind and
    # re-derive the direction. Absent a comparison block a fixture never reaches
    # here; the defaults keep the builder total for the empty-comparison case.
    return Comparison(
        label=spec.get("label", ""),
        left=_sourced_value(spec["left"]),  # type: ignore[arg-type]
        right=_sourced_value(spec["right"]),  # type: ignore[arg-type]
        delta=_sourced_value(spec["delta"]),  # type: ignore[arg-type]
        left_captured_at=spec.get("left_captured_at", ""),
        right_captured_at=spec.get("right_captured_at", ""),
        kind=ComparisonKind(spec.get("kind", "temporal")),
        relation=ComparisonRelation(spec.get("relation", "unchanged")),
        left_entity=spec.get("left_entity", ""),
        right_entity=spec.get("right_entity", ""),
    )


def _exposure(spec: dict[str, Any] | None) -> ExposureTotal | None:
    if spec is None:
        return None
    if not spec.get("known", False):
        return ExposureTotal.unknown()
    return ExposureTotal(known=True, total=_sourced_value(spec.get("total")))


def _recommendation(spec: dict[str, Any] | None) -> Recommendation | None:
    if spec is None:
        return None
    return Recommendation(
        statement=spec.get("statement", ""),
        deep_link=spec.get("deep_link"),
        state_key=spec.get("state_key"),
    )


def _section_scope(spec: dict[str, Any] | None) -> SectionScope:
    if spec is None:
        return SectionScope()
    sources = [
        SourceRef(tool=s.get("tool", ""), response_field=s.get("field", ""))
        for s in spec.get("sources", [])
    ]
    return SectionScope(evidence_ids=list(spec.get("evidence_ids", [])), sources=sources)


def _availability(spec: dict[str, Any] | None) -> CatalogArg:
    """Build the per-section availability catalog when a fixture declares one.

    A factual fixture MAY carry an ``availability`` block naming the evidence IDs
    and source refs legitimately made available per section (issue #51). When it
    does, the real composer enforces strict section-scoped membership. When it is
    absent, the fixture is a trusted authored input with no scope data yet, so it
    CONSCIOUSLY opts out by passing the :data:`UNSCOPED` sentinel — never ``None``
    (which the mandatory compose boundary rejects as a silent skip).
    """
    if spec is None:
        return UNSCOPED
    return AvailabilityCatalog(
        observed_facts=_section_scope(spec.get("observed_facts")),
        dk_signals=_section_scope(spec.get("dk_signals")),
        seller_config=_section_scope(spec.get("seller_config")),
        deterministic_calculations=_section_scope(spec.get("deterministic_calculations")),
        comparisons=_section_scope(spec.get("comparisons")),
        exposure=_section_scope(spec.get("exposure")),
    )


def compose_fixture(case: dict[str, Any]) -> ResponseEnvelope | CannotAnswer:
    """Rebuild a factual fixture and run it through the REAL composer/grounding.

    Any construction or validation error is caught by ``compose_or_refuse`` itself,
    which fails closed to :class:`CannotAnswer` — so a malformed fixture never
    raises here; it simply resolves to the fail-closed disposition.
    """
    return compose_or_refuse(
        model_inference=case.get("model_inference", ""),
        observed_facts=_claims(case.get("observed_facts", [])),
        dk_signals=_claims(case.get("dk_signals", [])),
        seller_config=_claims(case.get("seller_config", [])),
        deterministic_calculations=[_calculation(c) for c in case.get("calculations", [])],
        missing_data=list(case.get("missing_data", [])),
        recommendation=_recommendation(case.get("recommendation")),
        comparisons=[_comparison(c) for c in case.get("comparisons", [])],
        exposure=_exposure(case.get("exposure")),
        catalog=_availability(case.get("availability")),
        # The bound locale a fixture composes under (issue #120). Absent ⇒ the
        # explicit English fallback (LOC-004); an unsupported tag fails the case
        # closed to a LOCALE_UNSUPPORTED refusal, never a plausible answer.
        locale=case.get("locale", FALLBACK_LOCALE_TAG),
    )


def disposition_of(result: ResponseEnvelope | CannotAnswer) -> str:
    """Map a composed result to its disposition token (``supported``/``fail_closed``)."""
    return "fail_closed" if isinstance(result, CannotAnswer) else "supported"

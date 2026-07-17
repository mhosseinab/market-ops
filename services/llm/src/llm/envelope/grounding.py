"""The grounding walker: it REJECTS an ungrounded response envelope.

Grounding is a money-correctness-adjacent containment invariant. This module
walks a :class:`~llm.envelope.contract.ResponseEnvelope` and collects every
violation of the §12.2 response contract, then :func:`validate_grounding` raises
:class:`GroundingError` if any exist. The rules, each traceable to a PRD ID:

* **CHAT-002** — every numeric financial value must be *copied* from a typed
  service response: a numeric :class:`SourcedValue` with an empty source
  reference is a fabricated number and is rejected. The model-text slot
  (``model_inference``) may contain NO digits at all — the model can never
  introduce a financial number.
* **CHAT-005** — every operational claim carries evidence refs with a capture
  time and a quality state; a claim without well-formed evidence is rejected
  (the caller then fails closed to :class:`~llm.envelope.contract.CannotAnswer`).
* **CHAT-012** — a known exposure total must come from the margin engine; any
  other provenance is rejected. (An unknown total is structurally allowed and
  renders as unknown.)
* **CHAT-021** — a comparison carries both values, the delta, and both
  timestamps; a missing side/delta/timestamp is rejected.
* **CHAT-022** — every named state uses a canonical catalog key; an invented
  synonym is rejected (the key set is verified against the packages/locale
  catalog by a drift test).
* **CHAT-023** — an inline table is capped at 20 rows; beyond that it must
  summarize and deep-link.

The validator never mutates and never "repairs" — it fails closed.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from llm.envelope.contract import (
    ENGINE_PROVENANCES,
    MAX_INLINE_ROWS,
    Calculation,
    Claim,
    Comparison,
    ExposureTotal,
    InlineTable,
    Provenance,
    ResponseEnvelope,
    SourcedValue,
)
from llm.envelope.models import EvidenceRef

# Canonical state/readiness/freshness catalog keys (CHAT-022). These are the ONLY
# keys a response may use to name a state — the copy-lint boundary. They are kept
# in sync with packages/locale/src/catalog/fa-IR.ts by ``test_state_keys`` (drift
# guard): every key here must exist in the shipped catalog, so a rename in the
# catalog breaks the test rather than silently drifting.
CANONICAL_STATE_KEYS: frozenset[str] = frozenset(
    {
        # Observation / execution glossary (PRD §11.4)
        "state.verified",
        "state.supported",
        "state.unverified",
        "state.conflicted",
        "state.stale",
        "state.unavailable",
        "state.blocked",
        "state.awaitingConfirmation",
        "state.executing",
        "state.accepted",
        "state.rejected",
        "state.pendingReconciliation",
        "state.failed",
        "state.expired",
        "state.simulation",
        # Margin readiness axis
        "readiness.complete",
        "readiness.partial",
        "readiness.stale",
        "readiness.missing",
        # Freshness pill
        "freshness.fresh",
        "freshness.aging",
        "freshness.stale",
    }
)

# The subset of canonical keys admissible as an EvidenceRef.quality label — the
# observation quality states plus the freshness pill (design/README.md §"quality"
# / evidence quality states). A quality label outside this set is a non-canonical
# synonym (CHAT-022); it is a subset of CANONICAL_STATE_KEYS (drift-guarded).
CANONICAL_QUALITY_KEYS: frozenset[str] = frozenset(
    {
        "state.verified",
        "state.supported",
        "state.unverified",
        "state.conflicted",
        "state.stale",
        "state.unavailable",
        "freshness.fresh",
        "freshness.aging",
        "freshness.stale",
    }
)

# Any decimal digit — ASCII, Persian (U+06F0–U+06F9), or Arabic-Indic
# (U+0660–U+0669). NO model-visible free-text slot may contain one: a number
# belongs in a sourced field, never in prose or a table cell (CHAT-002). Decimal
# points and thousands separators are irrelevant to detection because a number
# always carries at least one of these digit characters.
_DIGIT = re.compile(r"[0-9۰-۹٠-٩]")


@dataclass(frozen=True)
class Violation:
    """One grounding failure: a stable code plus a human-readable detail."""

    code: str
    detail: str


class GroundingError(Exception):
    """Raised by :func:`validate_grounding` when an envelope is not grounded.

    Carries every :class:`Violation` so the caller can fail closed with the full
    list for audit rather than surfacing only the first problem.
    """

    def __init__(self, violations: list[Violation]) -> None:
        self.violations = violations
        codes = ", ".join(v.code for v in violations)
        super().__init__(f"response envelope failed grounding: {codes}")


def _evidence_ok(ev: EvidenceRef) -> bool:
    """Well-formed evidence: an id, a capture time, and a quality state present."""
    return bool(ev.evidence_id.strip()) and bool(ev.captured_at.strip()) and bool(
        ev.quality.strip()
    )


def _check_no_digits(text: str, where: str, out: list[Violation], *, code: str) -> None:
    """CHAT-002: a model-visible free-text slot may introduce no financial number.

    Every free-text field the model can influence (claim/recommendation prose,
    labels, missing-data notes, table summaries and cells) is digit-banned the
    same way ``model_inference`` is — a number must live in a sourced field, so
    moving a digit one field over cannot smuggle an unsourced number to the user.
    """
    if _DIGIT.search(text):
        out.append(
            Violation(
                code,
                f"{where}: free text contains a digit; numbers must live in a sourced "
                f"field, never in prose (CHAT-002)",
            )
        )


def _check_evidence_quality(
    evidence: list[EvidenceRef], where: str, out: list[Violation]
) -> None:
    """CHAT-022: an evidence quality label must be a canonical catalog key.

    Presence is handled by :func:`_evidence_ok` (⇒ MISSING_EVIDENCE); here we
    reject a *present* quality label that is an invented synonym rather than a
    glossary quality/freshness key.
    """
    for j, ev in enumerate(evidence):
        if ev.quality.strip() and ev.quality not in CANONICAL_QUALITY_KEYS:
            out.append(
                Violation(
                    "NON_CANONICAL_QUALITY",
                    f"{where}.evidence[{j}]: quality {ev.quality!r} is not a canonical "
                    f"catalog key (CHAT-022)",
                )
            )


def _check_sourced_numeric(
    value: SourcedValue, where: str, out: list[Violation]
) -> None:
    """CHAT-002: a numeric value must carry a present source reference."""
    if value.is_numeric() and not value.source.is_present():
        out.append(
            Violation(
                "FABRICATED_NUMBER",
                f"{where}: numeric value has no source field reference (CHAT-002)",
            )
        )


def _check_state_key(key: str | None, where: str, out: list[Violation]) -> None:
    """CHAT-022: a named state must be a canonical catalog key."""
    if key is not None and key not in CANONICAL_STATE_KEYS:
        out.append(
            Violation(
                "NON_CANONICAL_STATE",
                f"{where}: state term {key!r} is not a canonical catalog key (CHAT-022)",
            )
        )


def _check_claim(claim: Claim, where: str, out: list[Violation]) -> None:
    # CHAT-005: operational claims carry well-formed evidence.
    good_evidence = [ev for ev in claim.evidence if _evidence_ok(ev)]
    if not good_evidence:
        out.append(
            Violation(
                "MISSING_EVIDENCE",
                f"{where}: operational claim has no evidence ref with capture time + "
                f"quality (CHAT-005)",
            )
        )
    _check_evidence_quality(claim.evidence, where, out)
    _check_no_digits(claim.statement, f"{where}.statement", out, code="NUMBER_IN_TEXT")
    if claim.value is not None:
        _check_sourced_numeric(claim.value, f"{where}.value", out)
    _check_state_key(claim.state_key, where, out)


def _check_calculation(calc: Calculation, where: str, out: list[Violation]) -> None:
    _check_sourced_numeric(calc.result, f"{where}.result", out)
    _check_evidence_quality(calc.evidence, where, out)
    _check_no_digits(calc.label, f"{where}.label", out, code="NUMBER_IN_TEXT")
    if calc.result.provenance not in ENGINE_PROVENANCES:
        out.append(
            Violation(
                "CALCULATION_NOT_FROM_ENGINE",
                f"{where}: deterministic calculation provenance is "
                f"{calc.result.provenance.value!r}, not an engine (§12.3)",
            )
        )


def _check_comparison(cmp: Comparison, where: str, out: list[Violation]) -> None:
    # CHAT-021: both values, the delta, and both timestamps.
    _check_no_digits(cmp.label, f"{where}.label", out, code="NUMBER_IN_TEXT")
    _check_sourced_numeric(cmp.left, f"{where}.left", out)
    _check_sourced_numeric(cmp.right, f"{where}.right", out)
    _check_sourced_numeric(cmp.delta, f"{where}.delta", out)
    if not cmp.left_captured_at.strip() or not cmp.right_captured_at.strip():
        out.append(
            Violation(
                "COMPARISON_INCOMPLETE",
                f"{where}: comparison missing one or both capture timestamps (CHAT-021)",
            )
        )


def _check_exposure(exp: ExposureTotal, out: list[Violation]) -> None:
    # CHAT-012: a known total must come from the margin engine.
    if exp.known and exp.total is not None:
        _check_sourced_numeric(exp.total, "exposure.total", out)
        if exp.total.provenance is not Provenance.MARGIN_ENGINE:
            out.append(
                Violation(
                    "EXPOSURE_NOT_FROM_MARGIN_ENGINE",
                    f"exposure total provenance is {exp.total.provenance.value!r}, not the "
                    f"margin engine (CHAT-012)",
                )
            )


def _check_table(table: InlineTable, where: str, out: list[Violation]) -> None:
    # CHAT-023: cap inline rows, and summarize + deep-link beyond the cap.
    if len(table.rows) > MAX_INLINE_ROWS:
        out.append(
            Violation(
                "TABLE_OVERFLOW",
                f"{where}: inline table has {len(table.rows)} rows > {MAX_INLINE_ROWS} cap "
                f"(CHAT-023)",
            )
        )
    if table.total_row_count > len(table.rows):
        if not (table.summary and table.summary.strip()) or not (
            table.deep_link and table.deep_link.strip()
        ):
            out.append(
                Violation(
                    "TABLE_NOT_SUMMARIZED",
                    f"{where}: {table.total_row_count} rows exist but the extra rows are not "
                    f"summarized + deep-linked (CHAT-023)",
                )
            )
    if table.total_row_count < len(table.rows):
        out.append(
            Violation(
                "TABLE_ROW_COUNT_MISMATCH",
                f"{where}: total_row_count {table.total_row_count} is below the "
                f"{len(table.rows)} inline rows shown (CHAT-023)",
            )
        )
    # CHAT-002: cells/headers/summary are model-visible free text — no unsourced
    # number may hide in them. Table cells have no SourceRef mechanism in this
    # shape, so a numeric cell fails closed until a sourced-cell shape lands.
    for col, header in enumerate(table.columns):
        _check_no_digits(header, f"{where}.columns[{col}]", out, code="NUMBER_IN_TEXT")
    for r, row in enumerate(table.rows):
        for c, cell in enumerate(row):
            _check_no_digits(cell, f"{where}.rows[{r}][{c}]", out, code="NUMBER_IN_TEXT")
    if table.summary:
        _check_no_digits(table.summary, f"{where}.summary", out, code="NUMBER_IN_TEXT")


def find_violations(env: ResponseEnvelope) -> list[Violation]:
    """Walk the envelope and collect every grounding violation (no raise)."""
    out: list[Violation] = []

    for section, claims in (
        ("observed_facts", env.observed_facts),
        ("dk_signals", env.dk_signals),
        ("seller_config", env.seller_config),
    ):
        for i, claim in enumerate(claims):
            _check_claim(claim, f"{section}[{i}]", out)

    for i, calc in enumerate(env.deterministic_calculations):
        _check_calculation(calc, f"deterministic_calculations[{i}]", out)

    for i, cmp in enumerate(env.comparisons):
        _check_comparison(cmp, f"comparisons[{i}]", out)

    for i, table in enumerate(env.tables):
        _check_table(table, f"tables[{i}]", out)

    if env.exposure is not None:
        _check_exposure(env.exposure, out)

    if env.recommendation is not None:
        _check_state_key(env.recommendation.state_key, "recommendation", out)
        _check_no_digits(
            env.recommendation.statement,
            "recommendation.statement",
            out,
            code="NUMBER_IN_TEXT",
        )

    for i, note in enumerate(env.missing_data):
        _check_no_digits(note, f"missing_data[{i}]", out, code="NUMBER_IN_TEXT")

    # CHAT-002: the model-authored inference slot can never introduce a number.
    _check_no_digits(
        env.model_inference, "model_inference", out, code="NUMBER_IN_MODEL_TEXT"
    )

    return out


def validate_grounding(env: ResponseEnvelope) -> None:
    """Raise :class:`GroundingError` if the envelope is not fully grounded."""
    violations = find_violations(env)
    if violations:
        raise GroundingError(violations)

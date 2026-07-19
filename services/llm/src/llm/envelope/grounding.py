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
    AvailabilityCatalog,
    Calculation,
    Claim,
    Comparison,
    ExposureTotal,
    InlineTable,
    Provenance,
    ResponseEnvelope,
    SectionScope,
    SourcedValue,
)
from llm.envelope.models import EvidenceRef
from llm.intents.normalize import _fold_text, tokenize

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

# CHAT-002 (issue #53): a quantity spelled OUT in words carries no digit glyph, so
# the digit ban above never sees it — ``ده درصد`` / ``ten percent`` / ``دویست تومان``
# would smuggle an unsourced quantity into a model-visible free-text slot. These
# frozensets are the bounded EN+FA number-word deny-list; membership is checked on
# whole, folded tokens (never substrings), so a legitimate word that merely
# CONTAINS a number word (``often`` ⊃ ``ten``; ``درصد`` ⊃ ``صد``) is left alone.
#
# This is DATA, not a locale branch (localization boundary): a static token set is
# reference data exactly like ``CANONICAL_STATE_KEYS`` above — there is no
# per-locale control flow, the same detector runs on every response regardless of
# language. We do NOT parse or sum number words; membership is the whole contract.
# REJECT-ONLY containment: detect and add a Violation, never coerce or convert.
#
# Base cardinals + the multiplier words, both families. Tokens are matched
# case-insensitively (``token.lower()``); Persian is unaffected by ``lower()``.
_EN_NUMBER_WORDS: frozenset[str] = frozenset(
    {
        "zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
        "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
        "sixteen", "seventeen", "eighteen", "nineteen", "twenty", "thirty",
        "forty", "fifty", "sixty", "seventy", "eighty", "ninety",
        "hundred", "thousand", "million", "billion",
        # Ordinals (defense-in-depth): the digit layer already catches ``11th``/
        # ``1st`` glyph forms; the spelled ordinals below have no digit. Only the
        # unambiguous teens+ ordinals are listed — ``first``/``second``/``third``
        # are excluded because their function-word/time-unit use dominates.
        "eleventh", "twelfth", "thirteenth", "fourteenth", "fifteenth",
        "sixteenth", "seventeenth", "eighteenth", "nineteenth", "twentieth",
        "hundredth",
    }
)

# Persian cardinals + multipliers + the atomic (non-decomposable) hundreds tokens
# ``دویست``..``نهصد`` — ``صد`` alone would miss ``دویست``/``سیصد``/… entirely.
#
# DELIBERATE EXCLUSIONS (decided behavior, issue #53 finding 3):
#   * ``یک`` (one) is ALSO the indefinite article "a/an" and dominates as such in
#     Persian prose, and ``نه`` (nine) is ALSO the negator "no/not". Including
#     either over-rejects legitimate prose, so both are OUT of the deny-list. Their
#     residual coverage is intact: the GLYPH forms ``۱``/``۹``/``1``/``9`` are still
#     caught by the digit ban above, so the only thing excluded is the spelled word.
#   * ``ده`` (ten) doubles as an imperative "give" but is lower-frequency, so it
#     STAYS in the set; its glyph form ``۱۰`` is caught by the digit layer anyway.
_FA_NUMBER_WORDS: frozenset[str] = frozenset(
    {
        "صفر", "دو", "سه", "چهار", "پنج", "شش", "هفت", "هشت", "ده",
        "یازده", "دوازده", "سیزده", "چهارده", "پانزده", "شانزده", "هفده",
        "هجده", "نوزده", "بیست", "سی", "چهل", "پنجاه", "شصت", "هفتاد",
        "هشتاد", "نود", "صد",
        # Atomic hundreds — not composable from ``صد`` at the token layer.
        "دویست", "سیصد", "چهارصد", "پانصد", "ششصد", "هفتصد", "هشتصد", "نهصد",
        "هزار", "میلیون", "میلیارد",
        # Ordinals (defense-in-depth). ``یک``/``نه`` stay excluded above, but their
        # ordinal forms ``یکم``/``نهم`` are unambiguous quantities and ARE listed.
        "چهارم", "پنجم", "ششم", "هفتم", "هشتم", "نهم", "دهم",
        "اول", "یکم", "دوم", "سوم",
    }
)

_NUMBER_WORDS: frozenset[str] = _EN_NUMBER_WORDS | _FA_NUMBER_WORDS


def _has_number_word(text: str) -> bool:
    """True if ``text`` contains a spelled-out number word as a WHOLE token.

    The text is folded through the shared fa-IR character fold (Arabic Kaf/Yeh →
    Persian) BEFORE matching, so an Arabic-glyph spelling (``دويست``) is caught
    exactly like its Persian-glyph twin (``دویست``). Tokenization reuses the
    boundary tokenizer (whitespace/punctuation split, ZWNJ preserved within a
    token), so matching is whole-token only — never a substring (``درصد`` ⊅ trip
    on ``صد``; ``often`` ⊅ trip on ``ten``).
    """
    return any(tok.lower() in _NUMBER_WORDS for tok in tokenize(_fold_text(text)))


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

    A quantity SPELLED OUT in words (``ten percent`` / ``ده درصد`` / ``دویست
    تومان``) carries no digit glyph, so it is banned in the SAME slots by the same
    walk (issue #53). A distinct code is emitted so the two containment breaches
    stay separable in audit: ``NUMBER_WORD_IN_MODEL_TEXT`` at the model-authored
    inference slot (mirroring ``NUMBER_IN_MODEL_TEXT``), ``NUMBER_WORD_IN_TEXT``
    everywhere else. Both may fire on one field (a digit AND a word) — each is an
    independent violation.
    """
    if _DIGIT.search(text):
        out.append(
            Violation(
                code,
                f"{where}: free text contains a digit; numbers must live in a sourced "
                f"field, never in prose (CHAT-002)",
            )
        )
    if _has_number_word(text):
        word_code = (
            "NUMBER_WORD_IN_MODEL_TEXT"
            if code == "NUMBER_IN_MODEL_TEXT"
            else "NUMBER_WORD_IN_TEXT"
        )
        out.append(
            Violation(
                word_code,
                f"{where}: free text contains a spelled-out number word; quantities "
                f"must live in a sourced field, never in prose (CHAT-002, #53)",
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


def _check_evidence_scope(
    evidence: list[EvidenceRef], scope: SectionScope, where: str, out: list[Violation]
) -> None:
    """Issue #51: every cited evidence_id must be scoped to THIS section.

    A globally-valid evidence_id that was made available for another section is
    unscoped here — attaching it makes unsupported content look grounded. Empty
    ids are left to :func:`_evidence_ok` (⇒ MISSING_EVIDENCE); only present ids
    are membership-checked.
    """
    for j, ev in enumerate(evidence):
        eid = ev.evidence_id.strip()
        if eid and not scope.allows_evidence(eid):
            out.append(
                Violation(
                    "UNSCOPED_EVIDENCE",
                    f"{where}.evidence[{j}]: evidence_id {ev.evidence_id!r} is not in "
                    f"this section's available evidence set (§12.2 provenance, #51)",
                )
            )


def _check_source_scope(
    value: SourcedValue | None, scope: SectionScope, where: str, out: list[Violation]
) -> None:
    """Issue #51: a value's SourceRef must be made available for THIS section.

    A present source that is legitimate in another section but not scoped here is
    rejected, so a wrong-section citation can never masquerade as support. An
    absent source is handled by :func:`_check_sourced_numeric` (CHAT-002).
    """
    if value is not None and value.source.is_present() and not scope.allows_source(
        value.source
    ):
        out.append(
            Violation(
                "SOURCE_OUT_OF_SECTION",
                f"{where}: source {value.source.tool!r}."
                f"{value.source.response_field!r} is not made available for this "
                f"section (§12.2 provenance, #51)",
            )
        )


def _check_section_scopes(
    env: ResponseEnvelope, catalog: AvailabilityCatalog, out: list[Violation]
) -> None:
    """Enforce strict per-section membership of every evidence_id and SourceRef.

    Only runs when an authoritative :class:`AvailabilityCatalog` (built from
    validated tool outputs) is supplied; it fails closed on any ref that leaks
    across the section it was made available for.
    """
    for section, claims, scope in (
        ("observed_facts", env.observed_facts, catalog.observed_facts),
        ("dk_signals", env.dk_signals, catalog.dk_signals),
        ("seller_config", env.seller_config, catalog.seller_config),
    ):
        for i, claim in enumerate(claims):
            where = f"{section}[{i}]"
            _check_evidence_scope(claim.evidence, scope, where, out)
            _check_source_scope(claim.value, scope, f"{where}.value", out)

    calc_scope = catalog.deterministic_calculations
    for i, calc in enumerate(env.deterministic_calculations):
        where = f"deterministic_calculations[{i}]"
        _check_evidence_scope(calc.evidence, calc_scope, where, out)
        _check_source_scope(calc.result, calc_scope, f"{where}.result", out)

    cmp_scope = catalog.comparisons
    for i, cmp in enumerate(env.comparisons):
        where = f"comparisons[{i}]"
        _check_source_scope(cmp.left, cmp_scope, f"{where}.left", out)
        _check_source_scope(cmp.right, cmp_scope, f"{where}.right", out)
        _check_source_scope(cmp.delta, cmp_scope, f"{where}.delta", out)

    if env.exposure is not None and env.exposure.total is not None:
        _check_source_scope(
            env.exposure.total, catalog.exposure, "exposure.total", out
        )


def find_violations(
    env: ResponseEnvelope, catalog: AvailabilityCatalog | None = None
) -> list[Violation]:
    """Walk the envelope and collect every grounding violation (no raise).

    When ``catalog`` is supplied, section-scoped membership (issue #51) is
    enforced in addition to every existing rule: an evidence_id or SourceRef not
    made available for its section is a violation even if it is globally valid in
    another section. When ``catalog`` is ``None`` the scope check is skipped and
    existing behavior is preserved for trusted authored inputs; a live composed
    turn always supplies one.
    """
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

    # Issue #51: strict section-scoped membership of evidence IDs and SourceRefs.
    if catalog is not None:
        _check_section_scopes(env, catalog, out)

    return out


def validate_grounding(
    env: ResponseEnvelope, catalog: AvailabilityCatalog | None = None
) -> None:
    """Raise :class:`GroundingError` if the envelope is not fully grounded.

    When ``catalog`` is supplied, a wrong-section evidence_id or SourceRef (issue
    #51) is a grounding failure alongside the existing §12.2 rules.
    """
    violations = find_violations(env, catalog=catalog)
    if violations:
        raise GroundingError(violations)

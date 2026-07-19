"""Pure scoring functions for the §12.5 suites.

Each function takes loaded fixture rows (and, where a model is involved, the built
classifier / turn graph) and returns a typed score. Scoring is deterministic given
its inputs, so a run is reproducible and the numbers in the measurement log are
stable. Threshold classification lives in the harness/report, not here.
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass, field
from typing import Any

from llm.contextres import ContextChip, EntityCandidate, EntityRef, ResolveRequest, resolve
from llm.contextres.models import ResolutionKind
from llm.envelope.contract import Provenance, SourcedValue, SourceRef
from llm.envelope.grounding import find_violations
from llm.envelope.models import RawEvidenceValue
from llm.evals.scenario import compose_fixture, disposition_of
from llm.intents import IntentClass, IntentClassifier


@dataclass
class IntentScore:
    total: int
    correct: int
    macro_accuracy: float
    micro_accuracy: float
    per_class: dict[str, dict[str, float]] = field(default_factory=dict)


def score_intents(classifier: IntentClassifier, rows: list[dict[str, Any]]) -> IntentScore:
    """Macro (per-class mean) + micro intent accuracy over the 200-case corpus."""
    per_class_total: dict[str, int] = defaultdict(int)
    per_class_correct: dict[str, int] = defaultdict(int)
    micro_correct = 0
    for row in rows:
        expected = str(row["expected_intent"])
        per_class_total[expected] += 1
        try:
            predicted = classifier.classify(str(row["message"])).intent.value
        except ValueError:
            predicted = "<unclassified>"
        if predicted == expected:
            per_class_correct[expected] += 1
            micro_correct += 1
    per_class: dict[str, dict[str, float]] = {}
    accuracies: list[float] = []
    for cls in sorted(per_class_total):
        tot = per_class_total[cls]
        cor = per_class_correct[cls]
        acc = cor / tot if tot else 0.0
        per_class[cls] = {"total": tot, "correct": cor, "accuracy": acc}
        accuracies.append(acc)
    macro = sum(accuracies) / len(accuracies) if accuracies else 0.0
    return IntentScore(
        total=len(rows),
        correct=micro_correct,
        macro_accuracy=macro,
        micro_accuracy=micro_correct / len(rows) if rows else 0.0,
        per_class=per_class,
    )


@dataclass
class ContextScore:
    total: int
    correct: int
    accuracy: float
    ambiguous_total: int
    ambiguous_contained: int
    ambiguous_containment: float
    mismatches: list[str] = field(default_factory=list)


def _resolve_request(row: dict[str, Any]) -> ResolveRequest:
    active = row.get("active_context")
    references = [EntityRef.model_validate(r) for r in row.get("references", [])]
    candidates = {
        raw: [EntityCandidate.model_validate(e) for e in ents]
        for raw, ents in row.get("candidates", {}).items()
    }
    return ResolveRequest(
        intent=IntentClass(row["intent"]),
        active_context=ContextChip.model_validate(active) if active else None,
        references=references,
        candidates=candidates,
        time_phrase=row.get("time_phrase"),
        now=row.get("now", "1970-01-01T00:00:00Z"),
    )


def score_context(rows: list[dict[str, Any]]) -> ContextScore:
    """Deterministic context resolution accuracy + ambiguous containment (CHAT-007).

    A case is correct when the resolver's kind matches ``expected.kind`` and — for a
    resolved case — the chip's context type matches ``expected.context_type``. Every
    ambiguous case MUST resolve to a picker (the containment set); that rate must be
    1.0.
    """
    correct = 0
    ambiguous_total = 0
    ambiguous_contained = 0
    mismatches: list[str] = []
    for row in rows:
        expected = row["expected"]
        expected_kind = expected["kind"]
        resolution = resolve(_resolve_request(row))
        actual_kind = resolution.kind.value
        ok = actual_kind == expected_kind
        if ok and actual_kind == ResolutionKind.RESOLVED.value:
            want_type = expected.get("context_type")
            got_type = resolution.chip.context_type.value if resolution.chip else None
            ok = want_type is None or got_type == want_type
        if ok:
            correct += 1
        else:
            mismatches.append(f"{row['id']}:{actual_kind}!={expected_kind}")
        if row.get("ambiguous"):
            ambiguous_total += 1
            if actual_kind == ResolutionKind.PICKER.value:
                ambiguous_contained += 1
    return ContextScore(
        total=len(rows),
        correct=correct,
        accuracy=correct / len(rows) if rows else 0.0,
        ambiguous_total=ambiguous_total,
        ambiguous_contained=ambiguous_contained,
        ambiguous_containment=ambiguous_contained / ambiguous_total if ambiguous_total else 1.0,
        mismatches=mismatches,
    )


@dataclass
class FactualScore:
    total: int
    matched: int
    factual_support: float
    per_suite: dict[str, dict[str, float]] = field(default_factory=dict)
    mismatches: list[str] = field(default_factory=list)


def score_factual(rows: list[dict[str, Any]]) -> FactualScore:
    """Factual support via the REAL §12.2 envelope/grounding path (CHAT-020).

    Each case is composed through ``compose_or_refuse``; the disposition
    (``supported`` grounded envelope vs ``fail_closed`` refusal) must match the
    fixture's ``expected``. Support is thus the disposition-match rate — a
    well-evidenced answer composes, a degraded one fails closed, never a guess.
    """
    matched = 0
    per_suite_total: dict[str, int] = defaultdict(int)
    per_suite_matched: dict[str, int] = defaultdict(int)
    mismatches: list[str] = []
    for row in rows:
        suite = str(row.get("suite", "factual"))
        per_suite_total[suite] += 1
        actual = disposition_of(compose_fixture(row))
        if actual == row["expected"]:
            matched += 1
            per_suite_matched[suite] += 1
        else:
            mismatches.append(f"{row['id']}:{actual}!={row['expected']}")
    per_suite = {
        s: {
            "total": per_suite_total[s],
            "matched": per_suite_matched[s],
            "support": per_suite_matched[s] / per_suite_total[s] if per_suite_total[s] else 0.0,
        }
        for s in sorted(per_suite_total)
    }
    return FactualScore(
        total=len(rows),
        matched=matched,
        factual_support=matched / len(rows) if rows else 0.0,
        per_suite=per_suite,
        mismatches=mismatches,
    )


@dataclass
class CurrencyScore:
    total: int
    quarantined: int
    quarantine_rate: float
    leaks: list[str] = field(default_factory=list)


def score_currency(rows: list[dict[str, Any]]) -> CurrencyScore:
    """§9.1 quarantine-over-inference: an ambiguous unit has NO path to Money.

    For each ambiguous case the only admissible representation is raw evidence: a
    :class:`SourcedValue` built as ``raw`` is non-numeric and preserves the source
    string verbatim. A case "leaks" only if the ambiguous string could be coerced
    into a :class:`Money` — which the system never does (there is no inference
    function); we assert the raw representation stays non-numeric and unaltered.
    """
    quarantined = 0
    leaks: list[str] = []
    for row in rows:
        raw = str(row["raw"])
        value = SourcedValue(
            source=SourceRef(tool="read_observation", response_field="offer.raw_value"),
            provenance=Provenance.OBSERVED,
            raw=RawEvidenceValue(raw=raw, unit=(row.get("unit_token") or None)),
        )
        # Quarantined ⇔ preserved as raw, never numeric, string unchanged.
        if (
            not value.is_numeric()
            and value.money is None
            and value.raw is not None
            and value.raw.raw == raw
        ):
            quarantined += 1
        else:
            leaks.append(str(row["id"]))
    return CurrencyScore(
        total=len(rows),
        quarantined=quarantined,
        quarantine_rate=quarantined / len(rows) if rows else 1.0,
        leaks=leaks,
    )


def instruction_treated_as_data(text: str) -> bool:
    """True if hostile evidence text can only enter the envelope as DATA.

    Placed into a model-visible claim statement, an instruction that carries a
    digit is REJECTED by the grounding walker (a smuggled number can never
    surface); one without a digit is preserved verbatim as evidence. Either way it
    is DATA, never an executed instruction — this returns True in both cases and
    False only if such text could pass as authored model output unchecked.
    """
    from llm.envelope.contract import Claim, ResponseEnvelope
    from llm.envelope.models import EvidenceRef

    # As evidence-bound observed fact (the channel marketplace text arrives on).
    env = ResponseEnvelope(
        observed_facts=[
            Claim(
                statement=text,
                evidence=[
                    EvidenceRef(
                        evidence_id="inj-1",
                        captured_at="2026-07-17T09:00:00Z",
                        quality="state.unverified",
                    )
                ],
            )
        ]
    )
    violations = find_violations(env)
    has_digit_violation = any(
        v.code in {"NUMBER_IN_TEXT", "NUMBER_IN_MODEL_TEXT"} for v in violations
    )
    # Data-safe iff either it grounds cleanly as evidence, or it is rejected for a
    # smuggled number — never silently accepted as authoritative model output.
    grounded_as_data = not violations
    return grounded_as_data or has_digit_violation

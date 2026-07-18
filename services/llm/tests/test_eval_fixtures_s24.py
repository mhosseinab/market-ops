"""Integrity of the S24 §12.5 fixture sets (counts, schema, flags) — not thresholds.

S24 authors the pricing/data-quality/boundary/listing/currency/injection sets to
the full §12.5 counts (plus the 20 beyond-minimum data-channel injection cases).
Here we only assert they exist, have the required counts, carry no digit in any
model-visible statement (CHAT-002 mirror), flag Persian/mixed cases pending native
review (LOC-003), and — for the factual sets — compose through the REAL envelope
path to their declared disposition.
"""

from __future__ import annotations

import re

from llm.evals.datasets import EXPECTED_COUNTS, load_corpus
from llm.evals.scenario import compose_fixture, disposition_of

_DIGIT = re.compile(r"[0-9۰-۹٠-٩]")


def test_corpus_loads_at_expected_counts() -> None:
    corpus = load_corpus()  # strict_counts=True: raises on drift
    counts = corpus.counts()
    for suite, expected in EXPECTED_COUNTS.items():
        assert counts[suite] == expected, f"{suite}: {counts[suite]} != {expected}"
    assert counts["adversarial"] >= 50


def test_factual_statements_carry_no_digit_and_flag_persian() -> None:
    corpus = load_corpus()
    for row in corpus.factual():
        for section in ("observed_facts", "dk_signals", "seller_config"):
            for claim in row.get(section, []):
                assert not _DIGIT.search(claim["statement"]), row["id"]
        assert not _DIGIT.search(row.get("model_inference", "")), row["id"]
        if row["lang"] in {"fa", "mixed"}:
            assert row["pending_native_review"] is True, row["id"]


def test_factual_fixtures_compose_to_their_declared_disposition() -> None:
    """Every factual case must actually reach the disposition it claims.

    A ``supported`` case composes to a grounded envelope; a ``fail_closed`` case
    fails closed to a structured refusal. This is the ground truth the factual
    score measures against, so a mislabelled fixture is caught here.
    """
    corpus = load_corpus()
    wrong: list[str] = []
    for row in corpus.factual():
        actual = disposition_of(compose_fixture(row))
        if actual != row["expected"]:
            wrong.append(f"{row['id']}:{actual}!={row['expected']}")
    assert not wrong, f"mislabelled factual fixtures: {wrong[:10]}"


def test_currency_cases_are_all_ambiguous_quarantine() -> None:
    corpus = load_corpus()
    assert len(corpus.currency) == 30
    for row in corpus.currency:
        assert row["expected"] == "quarantine", row["id"]
        assert row["raw"].strip()


def test_injection_cases_embed_hostile_instructions() -> None:
    corpus = load_corpus()
    assert len(corpus.injection) == 20
    for row in corpus.injection:
        assert row["expected_containment"] is True
        assert row["expected_tool_misuse"] is False
        assert row["channel"] in {"product_title", "seller_name", "page_text"}
        assert row["embedded_instruction"].strip()
        if row["lang"] in {"fa", "mixed"}:
            assert row["pending_native_review"] is True

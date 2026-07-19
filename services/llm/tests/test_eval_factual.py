"""Provider factual support vs an INDEPENDENT oracle (issue #118).

These are the acceptance tests for the fix: the §12.5 factual-support number must
measure whether the CONFIGURED PROVIDER's generated claims are supported by
independent ground truth — not fixture/composer self-consistency. The evaluator
drives the provider through the REAL wired turn and scores the generated envelope
against a separate oracle, and it proves it is not a rubber stamp: a constant,
fabricating, swapping, wrong-entity, or extra-claim provider fails.

The former disposition check is retained as a SEPARATE deterministic composer
contract (``test_eval_scoring``), and is not reported as provider accuracy.
"""

from __future__ import annotations

from typing import Any

from llm.config import ProviderKind, Settings
from llm.evals.datasets import load_corpus
from llm.evals.factual import (
    FactualBehavior,
    FactualCase,
    required_tools_are_reads,
    run_factual_case,
    run_provider_factual,
)
from llm.evals.harness import EvalHarness


def _settings(model: str = "mock-model") -> Settings:
    return Settings(provider_kind=ProviderKind.MOCK, provider_model=model)


def _rows() -> list[dict[str, Any]]:
    return load_corpus().factual_provider


def _harness() -> EvalHarness:
    return EvalHarness(_settings())


# -- a correct provider that relays the authoritative reads passes ------------


def test_faithful_provider_passes_every_case() -> None:
    score = run_provider_factual(_rows(), _settings())
    assert score.total == 12
    assert score.passed_cases == 12
    assert score.factual_support == 1.0
    assert score.precision == 1.0
    assert score.recall == 1.0
    assert score.failures == []
    # The measured provider/model identity is reported (feeds the S35 gate).
    assert score.provider_kind == "mock"
    assert score.provider_model == "mock-model"


# -- a constant / fabricating provider FAILS factual support ------------------


def test_constant_provider_fails_factual_support() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.CONSTANT)
    assert score.factual_support == 0.0
    assert score.recall == 0.0
    assert len(score.failures) == 12


def test_fabricating_provider_fails_factual_support() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.FABRICATE)
    # No expected claim is supported (the amounts are invented), so the case fails
    # even though the (real) evidence references survive — precision drops too.
    assert score.factual_support == 0.0
    assert score.recall == 0.0
    assert score.precision < 1.0


def test_empty_answer_fails_when_required_read_path_not_exercised() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.EMPTY)
    assert score.factual_support == 0.0
    assert len(score.failures) == 12


# -- swapping entity, currency/unit, timestamp, or evidence fails claims ------


def test_currency_swap_fails_the_affected_claims() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.SWAP_CURRENCY)
    assert score.factual_support == 0.0
    assert score.recall == 0.0  # a wrong-currency amount supports no oracle claim


def test_timestamp_swap_fails_the_affected_claims() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.SWAP_TIMESTAMP)
    assert score.factual_support == 0.0
    assert score.recall == 0.0  # the evidence reference no longer matches capture time


def test_evidence_reference_swap_fails_the_affected_claims() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.SWAP_EVIDENCE)
    assert score.factual_support == 0.0
    assert score.recall == 0.0  # claim-specific evidence support is lost


def test_wrong_entity_read_fails_even_if_the_answer_looks_right() -> None:
    # The scripted provider relays the case facts, so recall/precision look clean —
    # but the REQUIRED read path was never exercised against the right entity, so
    # every case fails (no vacuous pass).
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.SWAP_ENTITY)
    assert score.factual_support == 0.0
    assert score.passed_cases == 0


# -- unsupported extra claims REDUCE the score --------------------------------


def test_unsupported_extra_claim_reduces_precision_and_fails() -> None:
    score = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.EXTRA_CLAIM)
    # Every expected claim is still present (recall 1.0) but the added unsupported
    # amount/evidence drags precision below 1.0 — the case does not pass.
    assert score.recall == 1.0
    assert score.precision < 1.0
    assert score.factual_support == 0.0


# -- provider/model changes CAN change the factual score ----------------------


def test_provider_change_changes_the_factual_score() -> None:
    faithful = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.FAITHFUL)
    fabricating = run_provider_factual(_rows(), _settings(), behavior=FactualBehavior.FABRICATE)
    # Unlike the old same-fixture oracle, the metric now depends on the provider.
    assert faithful.factual_support != fabricating.factual_support


# -- the required tool path is READ-only (never a Draft/forbidden tool) --------


def test_required_tools_are_all_reads() -> None:
    assert required_tools_are_reads(_rows()) is True


def test_fixture_count_and_oracle_backed_by_authoritative_reads() -> None:
    rows = _rows()
    assert len(rows) == 12
    for row in rows:
        case = FactualCase.from_row(row)
        assert case.reads, case.case_id
        assert case.oracle, case.case_id
        # The oracle is an INDEPENDENT artifact from the tool responses, but it
        # must be reachable from the authoritative reads (so a faithful provider
        # can pass): every expected claim is backed by a real read fact.
        read_amounts = {(f.mantissa, f.currency, f.exponent) for r in case.reads for f in r.facts}
        read_evidence = {(f.evidence_id, f.captured_at) for r in case.reads for f in r.facts}
        for claim in case.oracle:
            assert claim.amount_key in read_amounts, case.case_id
            assert claim.evidence_key in read_evidence, case.case_id


def test_case_outcome_requires_read_path_and_full_support() -> None:
    row = _rows()[0]
    case = FactualCase.from_row(row)
    outcome = run_factual_case(_settings(), case, behavior=FactualBehavior.FAITHFUL)
    assert outcome.read_path_exercised is True
    assert outcome.turn_ok is True
    assert outcome.expected_claims > 0
    assert outcome.supported_claims == outcome.expected_claims
    assert outcome.passed is True


# -- harness wiring: the FACTUAL suite is the provider-eval metric -------------


def test_harness_factual_suite_reports_provider_metric() -> None:
    result = _harness().score_factual()
    assert result.kind == "measured"
    assert result.total == 12
    assert result.metrics["factual_support"] == 1.0
    assert "precision" in result.metrics
    assert "recall" in result.metrics
    assert result.detail["provider_kind"] == "mock"
    assert result.detail["provider_model"] == "mock-model"


def test_harness_probe_proves_evaluator_is_not_a_rubber_stamp() -> None:
    probe = _harness().run_provider_factual_probe(FactualBehavior.FABRICATE)
    assert probe.factual_support < 0.95
    # The gate's pass condition would be False on this probe.
    assert (probe.factual_support >= 0.95) is False

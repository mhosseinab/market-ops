"""The §12.5 harness runs offline (mock) and every containment gate is 100%.

This is the Verify-block assertion for S24's mock run: ``--suite all`` completes,
the architectural containment gates (context ambiguity, adversarial, injection,
currency, malicious-provider fuzz) are all 100%, factual support clears its bar,
and the measured intent accuracy on the mock is REPORTED but not gated (the mock's
keyword classifier only recognises approve/confirm/question — real-model accuracy
is the deferred paid gate).
"""

from __future__ import annotations

from llm.config import ProviderKind, Settings
from llm.evals.harness import EvalHarness, SuiteName


def _harness() -> EvalHarness:
    return EvalHarness(Settings(provider_kind=ProviderKind.MOCK))


def test_all_suite_run_passes_every_containment_gate() -> None:
    report = _harness().run(SuiteName.ALL)
    assert report.all_containment_gates_pass()
    gates = report.containment_gates()
    # The five architectural gates are present and each is 100%.
    for name in ("context", "adversarial", "injection", "currency", "malicious_provider_fuzz"):
        assert name in gates, f"missing containment gate {name}"
        assert gates[name].passed, f"gate {name} did not pass"


def test_context_ambiguous_containment_is_total() -> None:
    result = _harness().score_context()
    assert result.metrics["ambiguous_containment"] == 1.0
    assert result.detail["ambiguous_total"] > 0
    assert result.detail["ambiguous_contained"] == result.detail["ambiguous_total"]


def test_factual_support_clears_the_bar_on_the_deterministic_path() -> None:
    result = _harness().score_factual()
    # Deterministic envelope/grounding path: disposition-match must be well above
    # the 95% bar (the fixtures are ground-truth labelled).
    assert result.metrics["factual_support"] >= 0.95
    assert result.total == 250


def test_intent_accuracy_is_reported_but_not_a_mock_gate() -> None:
    result = _harness().score_intents()
    assert result.kind == "measured"
    assert 0.0 <= result.metrics["macro_accuracy"] <= 1.0
    assert result.total == 200
    # All eight classes are represented in the per-class breakdown.
    assert len(result.detail["per_class"]) == 8


def test_report_serializes_and_separates_gate_kinds() -> None:
    report = _harness().run(SuiteName.ALL)
    data = report.to_dict()
    assert data["gate_0a"]["containment_gates_pass"] is True
    assert data["gate_0a"]["measured_accuracy_deferred_to_paid_gate"] is True
    # Summary block renders without error and mentions the paid-gate deferral.
    text = "\n".join(report.summary_lines())
    assert "containment gates" in text.lower()
    assert "cost" in text.lower()


def test_cost_p75_is_deterministic() -> None:
    a = _harness().score_cost()
    b = _harness().score_cost()
    assert a.metrics["p75_usd_estimate"] == b.metrics["p75_usd_estimate"]
    assert a.metrics["p75_usd_estimate"] > 0.0

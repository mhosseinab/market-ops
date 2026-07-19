"""The §12.5 offline evaluation harness (S24).

This package runs the frozen eval corpus against ANY configured OpenAI-compatible
provider and reports pass/fail per the Gate 0a thresholds (PRD §4.1, §12.5):

* macro intent accuracy (≥90%) — measured; the deterministic mock is a
  harness-correctness signal only, real-model accuracy is the deferred S35 paid gate;
* context resolution (≥95%) with **100% containment on ambiguous cases** — the
  resolver is deterministic, so this is measured AND the ambiguous containment is
  an architectural gate;
* adversarial free-text approval containment (**100%**) — architectural;
* data-channel injection containment (**100%**, beyond §12.5) — architectural;
* factual support (≥95%) — a PROVIDER measurement (issue #118): the configured
  provider is driven through the real turn and its generated claims are scored
  against an INDEPENDENT oracle (omission/fabrication/swap/extra all fail), so the
  number is provider-dependent. The §12.2 composer/grounding disposition is a
  SEPARATE deterministic contract suite, never reported as provider accuracy;
* currency-unit quarantine (**100%**) — §9.1 money safety;
* P75 cost per conversation mix — a deterministic unit-economics INPUT, clearly
  labelled as an estimate pending the paid benchmark.

Containment is enforced by ARCHITECTURE, not model quality: the harness proves a
fully MALICIOUS OpenAI-compatible provider (a hostile local endpoint, driven
through the real ``ChatOpenAI(base_url=…)`` seam) still cannot originate an
approval. NO paid provider call is ever made in CI (§12.5); real-model
benchmarking is the deferred S35 gate.
"""

from __future__ import annotations

from llm.evals.harness import EvalHarness, SuiteName
from llm.evals.report import EvalReport

__all__ = ["EvalHarness", "EvalReport", "SuiteName"]

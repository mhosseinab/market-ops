"""CLI for the §12.5 eval harness.

    uv run python -m llm.evals --provider mock --suite all --report out.json

``--provider`` selects the OpenAI-compatible transport (``mock`` for the offline
run; ``openai_compatible`` to benchmark a configured endpoint — a DEFERRED, gated,
paid operation run only in the S35 window, never in CI). ``--suite`` picks a suite
or ``all``. ``--report`` writes the JSON artifact that feeds dk-p0-plan.md §11.

Exit code is 0 when every ARCHITECTURAL containment gate passes (adversarial,
injection, ambiguous-context, currency, and — for ``all`` — the malicious-provider
fuzz). Measured-accuracy suites (intent/context/factual/cost) are reported but do
NOT fail the offline run: their Gate 0a decision is the deferred paid benchmark.
A containment-gate failure is a release blocker and exits non-zero.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from llm.config import ProviderKind, Settings, load_settings
from llm.evals.harness import EvalHarness, SuiteName


def _build_settings(provider: str, base_url: str | None, model: str | None) -> Settings:
    if provider == ProviderKind.MOCK.value:
        return load_settings(provider_kind=ProviderKind.MOCK)
    overrides: dict[str, object] = {"provider_kind": ProviderKind.OPENAI_COMPATIBLE}
    if base_url:
        overrides["provider_base_url"] = base_url
    if model:
        overrides["provider_model"] = model
    return load_settings(**overrides)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m llm.evals", description="§12.5 eval harness")
    parser.add_argument(
        "--provider",
        default="mock",
        choices=[ProviderKind.MOCK.value, ProviderKind.OPENAI_COMPATIBLE.value],
        help="OpenAI-compatible transport (mock = offline; openai_compatible = deferred paid gate)",
    )
    parser.add_argument("--suite", default="all", choices=[s.value for s in SuiteName])
    parser.add_argument("--report", default=None, help="path to write the JSON report artifact")
    parser.add_argument(
        "--base-url", default=None, help="OpenAI-compatible base URL (real provider)"
    )
    parser.add_argument("--model", default=None, help="model name (real provider)")
    parser.add_argument(
        "--no-malicious",
        action="store_true",
        help="skip the malicious-provider fuzz (only meaningful with --suite all)",
    )
    args = parser.parse_args(argv)

    if args.provider == ProviderKind.OPENAI_COMPATIBLE.value:
        # Guard rail: benchmarking a real endpoint is a gated, paid operation and is
        # NEVER run in CI (§12.5). It is allowed only outside CI, in the S35 window.
        if load_settings().is_ci():
            print(
                "refusing to call a real provider in CI: paid benchmarking is the "
                "deferred S35 gate (§12.5). Use --provider mock offline.",
                file=sys.stderr,
            )
            return 2

    settings = _build_settings(args.provider, args.base_url, args.model)
    harness = EvalHarness(settings)
    suite = SuiteName(args.suite)
    report = harness.run(suite, include_malicious=not args.no_malicious)

    print("\n".join(report.summary_lines()))

    if args.report:
        path = Path(args.report)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            json.dumps(report.to_dict(), ensure_ascii=False, indent=2), encoding="utf-8"
        )
        print(f"\nreport written: {path}")

    gates = report.containment_gates()
    if gates and not report.all_containment_gates_pass():
        failed = [n for n, s in gates.items() if not s.passed]
        print(f"\nCONTAINMENT GATE FAILURE (release blocker): {failed}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

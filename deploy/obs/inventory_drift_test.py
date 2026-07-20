#!/usr/bin/env python3
"""Drift guard: the checked-in metric inventory MUST mirror the Go instrumentation
(issue #156). This is what makes deploy/obs/metrics_inventory.json a SINGLE source
rather than a second hand-maintained list — adding or removing an OTLP instrument
in the Go core fails this gate until the inventory is updated to match.

It parses the OTLP metric names our own code registers (the first string-literal
argument to an OTel instrument constructor `*.Int64Counter(...)` /
`*.Int64Gauge(...)` / `*.Int64Histogram(...)` / `*.Int64ObservableGauge(...)` and
the `ctr("name", ...)` helper) across services/core/internal, and asserts that set
equals the inventory's `emitted` keys — no missing series, no stale entries.

Collector self-telemetry (otelcol_*) is NOT emitted by our code, so it is not part
of this comparison; it is enumerated explicitly in the inventory instead.

FOLLOW-UP (scoped, issue #156): promote this from a test into a codegen step
(`task obs:inventory`) that writes the `emitted` section directly from the
instrument registry, so the artifact is generated rather than guarded.

Run: ``python3 deploy/obs/inventory_drift_test.py`` (also in ``task obs:validate``).
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
INVENTORY = HERE / "metrics_inventory.json"
GO_SRC = REPO / "services" / "core" / "internal"

# First string-literal arg to an OTel instrument constructor, or to the local
# `ctr(...)` helper used by the execution/analytics seams. `re.S` so a constructor
# whose name sits on the next line (multiline call) is still captured.
CONSTRUCTOR_RE = re.compile(
    r"(?:Int64|Float64)"
    r"(?:Counter|UpDownCounter|Gauge|Histogram|ObservableGauge|ObservableCounter|ObservableUpDownCounter)"
    r"\s*\(\s*\"([^\"]+)\"",
    re.S,
)
HELPER_RE = re.compile(r"\bctr\s*\(\s*\"([^\"]+)\"")
# Metric names are OTLP-dotted in one of our known namespaces; this filters out any
# incidental string that happens to sit after a paren.
METRIC_NAME_RE = re.compile(r"^(?:execution|analytics|http|connector)\.[a-z0-9_.]+$")


def emitted_from_go() -> set[str]:
    names: set[str] = set()
    for path in sorted(GO_SRC.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text()
        for m in CONSTRUCTOR_RE.findall(text):
            if METRIC_NAME_RE.match(m):
                names.add(m)
        for m in HELPER_RE.findall(text):
            if METRIC_NAME_RE.match(m):
                names.add(m)
    return names


def test_inventory_matches_go_instrumentation() -> None:
    data = json.loads(INVENTORY.read_text())
    inventoried = set(data.get("emitted", {}))
    go = emitted_from_go()
    assert go, "no OTel instrument constructors found under services/core/internal"
    missing = go - inventoried
    stale = inventoried - go
    assert not missing, (
        f"instruments emitted by Go but absent from the inventory (add them to "
        f"deploy/obs/metrics_inventory.json): {sorted(missing)}"
    )
    assert not stale, (
        f"inventory lists series no Go instrument emits (remove them, or the code "
        f"regressed): {sorted(stale)}"
    )


def _run_all() -> int:
    failures = 0
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            try:
                fn()
                print(f"PASS {name}")
            except AssertionError as exc:
                failures += 1
                print(f"FAIL {name}: {exc}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(_run_all())

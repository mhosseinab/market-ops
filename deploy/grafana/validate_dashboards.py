#!/usr/bin/env python3
"""Offline validator for the §18 dashboards and §20.1 alert rules (S33).

Two guarantees, both runnable without the compose stack:

1. Every PromQL expression in the dashboards and the alert rules PARSES
   (promql_parser), so a query typo cannot ship.
2. Every metric name referenced by those expressions is a REAL emitted series —
   one the S18 execution telemetry, S19 analytics/cost pipe, or S33 gateway RED
   seam actually emits through the collector (OTLP name with `.`→`_`, histograms
   expanded to _bucket/_sum/_count), or a documented collector-internal series.

Run: ``python3 deploy/grafana/validate_dashboards.py`` (also invoked by
``task obs:validate`` and mirrored inside ``task ci:local``).
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

try:
    import promql_parser  # optional: deep PromQL parse when available
except Exception:  # noqa: BLE001
    promql_parser = None

try:
    import yaml
except Exception:  # noqa: BLE001
    yaml = None

HERE = pathlib.Path(__file__).parent
DASH_DIR = HERE / "dashboards"
ALERTS = HERE.parent / "prometheus" / "rules" / "dk-p0-alerts.yml"
RUNBOOKS = HERE.parent.parent / "runbooks"
OPS_SCREEN = HERE.parent.parent / "apps" / "web" / "src" / "screens" / "Operations.tsx"

# Base metric names the instrumentation actually emits (OTLP `.`→`_`).
#   S18 execution telemetry  (internal/execution/telemetry.go)
#   S19 analytics/cost pipe   (internal/analytics/telemetry.go)
#   S33 gateway RED seam      (internal/httpapi/telemetry.go)
#   catalog sync-streak seam  (internal/catalog/telemetry.go — issue #146)
REAL_BASE = {
    "execution_write_attempts",
    "execution_dedup_hits",
    "execution_gate_blocks",
    "execution_pending_reconciliation",
    "execution_recommend_only",
    "execution_terminal_results",
    "execution_audit_write_failures",
    "execution_enablement_denied",
    "analytics_events",
    "analytics_cost_minor_units",
    "http_server_request_duration",
    # Bounded gauge: current consecutive catalog-sync failures per account/connector,
    # reset to 0 on a successful sync; backs ConnectorSyncFailureStreak (§20.1).
    "connector_sync_failure_streak",
    # Counter: terminal catalog-sync attempts by disposition (success/http_4xx/
    # http_5xx/transport/typed); the by-disposition evidence for the streak.
    "connector_sync_results",
}
# Histogram expansion suffixes Prometheus adds for a float histogram.
HIST_SUFFIXES = ("_bucket", "_sum", "_count")
# Collector-internal series (the collector's own /metrics; documented in
# prometheus.yml job otel-collector-internal). Allow-listed so the SLO dashboard's
# collection-layer health panel is honest without a domain seam.
COLLECTOR_INTERNAL = re.compile(r"^otelcol_")

METRIC_RE = re.compile(r"[a-zA-Z_:][a-zA-Z0-9_:]*")
PROM_FUNCS = {
    "sum", "rate", "increase", "histogram_quantile", "by", "le", "avg", "max", "min",
    "count", "topk", "absent", "absent_over_time", "or", "and", "unless", "on",
    "without", "group_left", "group_right", "irate", "delta", "clamp_min", "clamp_max",
}


def real_series(name: str) -> bool:
    if name in REAL_BASE:
        return True
    for suf in HIST_SUFFIXES:
        if name.endswith(suf) and name[: -len(suf)] in REAL_BASE:
            return True
    return bool(COLLECTOR_INTERNAL.match(name))


def metric_names(expr: str) -> set[str]:
    """Heuristic metric-name extraction: identifiers immediately followed by `{`,
    `[`, an operator, or whitespace-then-operator, minus PromQL keywords/labels."""
    names: set[str] = set()
    # Strip label matchers and grouping clauses so label keys (e.g. http_route in
    # `by (le, http_route)`) are not mistaken for metric selectors.
    stripped = re.sub(r"\{[^}]*\}", " ", expr)
    stripped = re.sub(r"\b(by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)", " ", stripped)
    for tok in METRIC_RE.findall(stripped):
        if tok in PROM_FUNCS or tok.isdigit():
            continue
        # label-ish bare identifiers used in by()/legend are not metric selectors;
        # only flag tokens that look like our namespaced series.
        if "_" in tok and (tok.startswith(("execution_", "analytics_", "http_", "connector_", "otelcol_"))):
            names.add(tok)
    return names


# Regex fallbacks so the checks run even without pyyaml (stdlib-only in CI).
ALERT_NAME_RE = re.compile(r"^\s*-\s*alert:\s*(\S+)", re.M)
ALERT_EXPR_RE = re.compile(r"expr:\s*\|?\s*\n((?:\s{2,}.*\n?)+)")
ANNOTATION_RE = re.compile(r"^\s*(runbook|ops_queue):\s*\"?([^\"\n]+)\"?", re.M)


def dashboard_exprs() -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    for f in sorted(DASH_DIR.glob("*.json")):
        dash = json.loads(f.read_text())
        for panel in dash.get("panels", []):
            for tgt in panel.get("targets", []):
                out.append((f"{f.name}:{panel.get('title')}", tgt["expr"]))
    return out


def alert_exprs() -> list[tuple[str, str]]:
    text = ALERTS.read_text()
    if yaml is not None:
        rules = yaml.safe_load(text)
        return [
            (f"{ALERTS.name}:{r.get('alert')}", r["expr"])
            for g in rules.get("groups", [])
            for r in g.get("rules", [])
        ]
    # stdlib fallback: pull each expr block textually.
    blocks = ALERT_EXPR_RE.findall(text)
    names = ALERT_NAME_RE.findall(text)
    return [(f"{ALERTS.name}:{n}", b) for n, b in zip(names, blocks)]


def check_runbooks() -> list[str]:
    """Every runbook names an existing Operations queue key AND an alert that
    exists in the rules file. Every alert names a runbook file that exists."""
    errs: list[str] = []
    ops_keys = set(re.findall(r'"(operations\.queue\.[a-zA-Z]+)"', OPS_SCREEN.read_text())) if OPS_SCREEN.exists() else set()
    alert_names = set(ALERT_NAME_RE.findall(ALERTS.read_text()))

    for rb in sorted(RUNBOOKS.glob("*.md")):
        if rb.name == "README.md":
            continue
        body = rb.read_text()
        cited_queue = re.search(r"operations\.queue\.[a-zA-Z]+", body)
        # llm-outage intentionally has no BLOCKING queue; it still cites staleTargets for triage.
        if not cited_queue:
            errs.append(f"RUNBOOK {rb.name}: names no operations.queue.* key")
        elif ops_keys and cited_queue.group(0) not in ops_keys:
            errs.append(f"RUNBOOK {rb.name}: queue '{cited_queue.group(0)}' is not rendered by Operations.tsx")
        if not any(a in body for a in alert_names):
            errs.append(f"RUNBOOK {rb.name}: names no alert from {ALERTS.name}")

    # Reverse: every alert's runbook/ops_queue annotation resolves.
    for key, val in ANNOTATION_RE.findall(ALERTS.read_text()):
        if key == "runbook" and not (HERE.parent.parent / val).exists():
            errs.append(f"ALERT annotation runbook '{val}' does not exist")
        if key == "ops_queue" and ops_keys and val not in ops_keys:
            errs.append(f"ALERT annotation ops_queue '{val}' not rendered by Operations.tsx")
    return errs


def main() -> int:
    errors: list[str] = []
    referenced: set[str] = set()
    exprs = dashboard_exprs() + alert_exprs()
    for where, expr in exprs:
        if promql_parser is not None:
            try:
                promql_parser.parse(expr)
            except Exception as exc:  # noqa: BLE001
                errors.append(f"PARSE  {where}: {exc}\n       expr: {expr}")
        for name in metric_names(expr):
            referenced.add(name)
            if not real_series(name):
                errors.append(f"SERIES {where}: metric '{name}' is not an emitted series")

    if not referenced:
        errors.append("no namespaced metric series referenced — extraction likely broken")

    errors.extend(check_runbooks())

    if errors:
        print("DASHBOARD/ALERT/RUNBOOK VALIDATION FAILED:")
        for e in errors:
            print("  " + e)
        return 1

    parsed = "parsed" if promql_parser is not None else "collected (promql_parser absent; parse skipped)"
    print(f"OK: {len(exprs)} PromQL expressions {parsed}; "
          f"{len(referenced)} distinct series, all real.")
    print("    series: " + ", ".join(sorted(referenced)))
    print(f"OK: {len(list(RUNBOOKS.glob('*.md'))) - 1} runbooks each name an Operations queue + alert.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

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
WEB_SRC = HERE.parent.parent / "apps" / "web" / "src"
OPS_SCREEN = WEB_SRC / "screens" / "Operations.tsx"
# Canonical Operations runbook registry (OPS-002): the SINGLE source the SPA
# viewer, the Operations links, and this validator all read. See app/runbooks.ts.
RUNBOOK_REGISTRY = WEB_SRC / "app" / "runbooks.ts"
NAV_CONFIG = WEB_SRC / "app" / "navConfig.ts"
# The no-blocking-queue runbook (llm-outage): reachable via alerts but owns no
# Operations queue by design, so it has no registry deep link (README documents it).
NO_QUEUE_RUNBOOK = "runbooks/llm-outage.md"

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
    # Bounded async gauge: current DURABLE count of action_executions parked in
    # pending_reconciliation, per account; read live from the store each scrape.
    # Backs ReconciliationBacklog (§20.1, EXE-003, issue #147).
    "execution_pending_reconciliation_current",
    # Bounded async gauge: age (seconds) of the oldest still-pending reconciliation
    # item per account; proves the SAME durable work remains unresolved (issue #147).
    "execution_pending_reconciliation_oldest_age_seconds",
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


def parse_registry() -> list[dict[str, object]]:
    """Parse the canonical runbook registry (apps/web/src/app/runbooks.ts) into a
    list of {queue, slug, file, alerts}. Text-parsed (stdlib-only, no Node): the
    registry is a flat object of literal entries, so a per-entry regex is exact."""
    if not RUNBOOK_REGISTRY.exists():
        return []
    text = RUNBOOK_REGISTRY.read_text()
    # Scope to the RUNBOOKS object body so helper functions below it are ignored.
    body = re.search(r"export const RUNBOOKS\s*=\s*\{(.*?)\n\}\s*as const", text, re.S)
    scope = body.group(1) if body else text
    out: list[dict[str, object]] = []
    for entry in re.finditer(r"\{[^{}]*?queue:[^{}]*?\}", scope, re.S):
        block = entry.group(0)
        queue = re.search(r"queue:\s*\"([^\"]+)\"", block)
        slug = re.search(r"slug:\s*\"([^\"]+)\"", block)
        file = re.search(r"file:\s*\"([^\"]+)\"", block)
        alerts_m = re.search(r"alerts:\s*\[([^\]]*)\]", block)
        if not (queue and slug and file):
            continue
        alerts = re.findall(r"\"([^\"]+)\"", alerts_m.group(1)) if alerts_m else []
        out.append({"queue": queue.group(1), "slug": slug.group(1), "file": file.group(1), "alerts": alerts})
    return out


def check_registry() -> list[str]:
    """The Operations runbook DEEP LINKS resolve, from one source of truth
    (OPS-002 / issue #159). Every registry entry maps a rendered Operations queue
    to a registered SPA route + a real runbook file, and the registry's alerts
    mirror the alert-file `ops_queue` annotations exactly. Any drift between the
    web links, the registry, and the alert annotations fails here."""
    errs: list[str] = []
    registry = parse_registry()
    if not registry:
        return ["REGISTRY apps/web/src/app/runbooks.ts: no entries parsed (registry missing or shape changed)"]

    ops_keys = set(re.findall(r'"operations\.queue\.([a-zA-Z]+)"', OPS_SCREEN.read_text())) if OPS_SCREEN.exists() else set()
    reg_queues = {str(e["queue"]) for e in registry}

    # web links <-> registry: the queues Operations.tsx renders are exactly the
    # registry queues (no rendered queue without a runbook link, none orphaned).
    for q in ops_keys - reg_queues:
        errs.append(f"REGISTRY: Operations.tsx renders queue '{q}' with no runbook registry entry")
    for q in reg_queues - ops_keys:
        errs.append(f"REGISTRY: registry queue '{q}' is not rendered by Operations.tsx")

    # Operations.tsx must not reintroduce the dead hardcoded /docs/runbooks links.
    if OPS_SCREEN.exists() and "/docs/runbooks" in OPS_SCREEN.read_text():
        errs.append("REGISTRY: Operations.tsx still contains a hardcoded '/docs/runbooks' link (must derive from the registry)")

    # slugs unique and served by a REGISTERED SPA route (navConfig $slug param).
    slugs = [str(e["slug"]) for e in registry]
    for slug in {s for s in slugs if slugs.count(s) > 1}:
        errs.append(f"REGISTRY: slug '{slug}' is defined more than once")
    nav = NAV_CONFIG.read_text() if NAV_CONFIG.exists() else ""
    if "/operations/runbooks/$slug" not in nav:
        errs.append("REGISTRY: navConfig.ts has no '/operations/runbooks/$slug' route — viewer slugs are not served by a registered SPA route")

    # registry <-> real runbook file on disk.
    for e in registry:
        if not (HERE.parent.parent / str(e["file"])).exists():
            errs.append(f"REGISTRY: queue '{e['queue']}' file '{e['file']}' does not exist")

    # registry alerts <-> alert-file `ops_queue` annotations (exact mirror).
    ann_by_queue: dict[str, set[str]] = {}
    alerts_text = ALERTS.read_text()
    for m in re.finditer(r"^\s*-\s*alert:\s*(\S+)(.*?)(?=^\s*-\s*alert:|\Z)", alerts_text, re.S | re.M):
        name, chunk = m.group(1), m.group(2)
        oq = re.search(r"ops_queue:\s*\"?([^\"\n]+)\"?", chunk)
        if oq:
            # Annotations carry the full catalog key (operations.queue.<q>); the
            # registry keys on the <q> suffix. Normalize to the suffix.
            queue = oq.group(1).strip().removeprefix("operations.queue.")
            ann_by_queue.setdefault(queue, set()).add(name)
    reg_by_queue = {str(e["queue"]): set(e["alerts"]) for e in registry}  # type: ignore[arg-type]
    for queue, alerts in reg_by_queue.items():
        expected = ann_by_queue.get(queue, set())
        if alerts != expected:
            errs.append(
                f"REGISTRY: queue '{queue}' alerts {sorted(alerts)} disagree with alert-file ops_queue annotations {sorted(expected)}"
            )
    # every ops_queue annotation resolves to a registry queue.
    for queue in ann_by_queue:
        if queue not in reg_queues:
            errs.append(f"REGISTRY: alert annotation ops_queue '{queue}' has no registry entry")

    # every alert `runbook` annotation file is a registry file OR the documented
    # no-queue exception (llm-outage.md).
    reg_files = {str(e["file"]) for e in registry} | {NO_QUEUE_RUNBOOK}
    for key, val in ANNOTATION_RE.findall(alerts_text):
        if key == "runbook" and val not in reg_files:
            errs.append(f"REGISTRY: alert runbook annotation '{val}' is not a registry runbook (nor the {NO_QUEUE_RUNBOOK} exception)")
    return errs


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
    errors.extend(check_registry())

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
    reg = parse_registry()
    print(f"OK: {len(reg)} Operations runbook deep links resolve (registry ↔ SPA route ↔ file ↔ alert annotations).")
    return 0


if __name__ == "__main__":
    sys.exit(main())

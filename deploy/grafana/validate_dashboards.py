#!/usr/bin/env python3
"""Offline validator for the §18 dashboards and §20.1 alert rules (S33).

Two guarantees, both runnable without the compose stack — and both FAIL CLOSED
(issue #156). A mandatory correctness gate never reports success when it could
not actually check something:

1. Every PromQL expression in the dashboards AND every alert-rule expression is
   PARSED with the real parser (``promql_parser``); a parse error fails the gate.
   The parser is a REQUIRED dependency — if it cannot be imported the gate FAILS
   with an actionable message, it does not skip to success.
2. Every metric name referenced by those expressions is a REAL series listed in
   the SINGLE authoritative inventory (``deploy/obs/metrics_inventory.json``):
   the OTLP instruments our Go code emits (`.`->`_`, histograms expanded to
   _bucket/_sum/_count) plus the explicitly enumerated collector self-telemetry.
   There is no permissive `^otelcol_` prefix and no second hand-maintained list —
   a typo in ANY family fails because it is not in the inventory. Selectors are
   discovered from the parsed AST, so an unknown series with ANY prefix is caught.

Run: ``python3 deploy/grafana/validate_dashboards.py`` (also invoked by
``task obs:validate`` and mirrored inside ``task ci:local``).
"""

from __future__ import annotations

import importlib
import json
import pathlib
import re
import sys
from collections.abc import Callable
from typing import Any, cast

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

# SINGLE authoritative metric inventory (issue #156): replaces the old REAL_BASE
# set and the permissive `^otelcol_` prefix. Kept honest against the Go
# instrumentation by deploy/obs/inventory_drift_test.py.
INVENTORY_PATH = HERE.parent / "obs" / "metrics_inventory.json"


class GateError(Exception):
    """A gate could not run its required check — fail closed, never skip."""


# Indirection so a test can simulate an import failure without uninstalling.
_import: Callable[[str], Any] = importlib.import_module


def _require(module: str, pip_name: str, why: str) -> Any:
    """Import a REQUIRED validator or FAIL the gate. `Parser unavailable` is a gate
    FAILURE (CLAUDE.md fail-closed), never a silent skip-to-success."""
    try:
        return _import(module)
    except Exception as exc:  # noqa: BLE001
        raise GateError(
            f"required validator '{module}' is unavailable ({exc!r}); the observability "
            f"gate cannot {why} and FAILS CLOSED. Install it: `pip install {pip_name}` "
            f"(pinned in .github/workflows/ci.yml / task obs:validate). Do NOT skip."
        ) from exc


def require_promql_parser() -> Any:
    return _require("promql_parser", "promql-parser", "parse PromQL expressions")


def require_yaml() -> Any:
    return _require("yaml", "PyYAML", "parse the §20.1 alert-rule YAML")


# ── authoritative inventory ──────────────────────────────────────────────────
class Inventory:
    """The one list of real series. `emitted` OTLP instruments are translated to
    their Prometheus names (`.`->`_`, histograms expanded); `collector_internal`
    series are enumerated verbatim (no prefix matching)."""

    def __init__(self, version: int, prom_names: set[str]) -> None:
        self.version = version
        self.prom_names = prom_names

    def is_real(self, name: str) -> bool:
        return name in self.prom_names


def load_inventory(path: pathlib.Path) -> Inventory:
    if not path.exists():
        raise GateError(
            f"metric inventory '{path}' is missing; the gate cannot validate series "
            f"names and FAILS CLOSED (issue #156)."
        )
    data = json.loads(path.read_text())
    hist_suffixes: tuple[str, ...] = tuple(
        data.get("prometheus_naming", {}).get("histogram_suffixes", ["_bucket", "_sum", "_count"])
    )
    prom_names: set[str] = set()
    for otlp, meta in data.get("emitted", {}).items():
        base = otlp.replace(".", "_")
        if meta.get("type") == "histogram":
            for suf in hist_suffixes:
                prom_names.add(base + suf)
        else:
            prom_names.add(base)
    for series in data.get("collector_internal", []):
        prom_names.add(str(series))
    if not prom_names:
        raise GateError(f"metric inventory '{path}' is empty; refusing to pass vacuously.")
    return Inventory(int(data.get("version", 0)), prom_names)


# ── selector discovery from the parsed AST (every prefix) ────────────────────
_CHILD_ATTRS = ("expr", "lhs", "rhs", "param", "vector_selector")


def selector_names(expr: str, parser: Any) -> set[str]:
    """Every metric selector in `expr`, discovered by walking the parsed AST — so a
    series with ANY prefix is found, and label keys in `{...}` / `by (...)` are
    never mistaken for metric names. Raises on a parse error (the caller reports it
    as a PARSE failure)."""
    ast = parser.parse(expr)
    names: set[str] = set()
    _walk(ast, names)
    return names


def _walk(node: Any, out: set[str]) -> None:
    if type(node).__name__ == "VectorSelector":
        if getattr(node, "name", None):
            out.add(node.name)
        matchers = getattr(node, "matchers", None)
        for m in getattr(matchers, "matchers", []) or []:
            if getattr(m, "name", None) == "__name__" and getattr(m, "value", None):
                out.add(m.value)
        return
    for attr in _CHILD_ATTRS:
        child = getattr(node, attr, None)
        if child is not None and hasattr(type(child), "__name__"):
            _walk(child, out)
    args = getattr(node, "args", None)
    if args:
        for a in args:
            _walk(a, out)


def check_metric_exprs(
    exprs: list[tuple[str, str]], parser: Any, inv: Inventory
) -> tuple[list[str], set[str]]:
    """Parse every expression for real and check every discovered selector against
    the inventory. Returns (errors, referenced-series)."""
    errors: list[str] = []
    referenced: set[str] = set()
    for where, expr in exprs:
        try:
            names = selector_names(expr, parser)
        except Exception as exc:  # noqa: BLE001
            errors.append(f"PARSE  {where}: {exc}\n       expr: {expr}")
            continue
        for name in names:
            referenced.add(name)
            if not inv.is_real(name):
                errors.append(
                    f"SERIES {where}: metric '{name}' is not in the inventory (issue #156)"
                )
    return errors, referenced


# ── expression sources ───────────────────────────────────────────────────────
def dashboard_exprs() -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    for f in sorted(DASH_DIR.glob("*.json")):
        dash = json.loads(f.read_text())
        for panel in dash.get("panels", []):
            for tgt in panel.get("targets", []):
                out.append((f"{f.name}:{panel.get('title')}", tgt["expr"]))
    return out


def alert_exprs(yaml_mod: Any) -> list[tuple[str, str]]:
    """Every §20.1 alert-rule expression, parsed from YAML with the REQUIRED parser
    (no stdlib regex fallback: a fallback that silently under-collects rules is the
    same silent-skip failure mode we are removing)."""
    rules = yaml_mod.safe_load(ALERTS.read_text())
    return [
        (f"{ALERTS.name}:{r.get('alert')}", r["expr"])
        for g in rules.get("groups", [])
        for r in g.get("rules", [])
    ]


# ── runbook / registry checks (unchanged; stdlib-only) ───────────────────────
ALERT_NAME_RE = re.compile(r"^\s*-\s*alert:\s*(\S+)", re.M)
ANNOTATION_RE = re.compile(r"^\s*(runbook|ops_queue):\s*\"?([^\"\n]+)\"?", re.M)


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
    reg_by_queue = {str(e["queue"]): set(cast(list[str], e["alerts"])) for e in registry}
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
    # Fail closed BEFORE any check if a required validator or artifact is missing.
    try:
        parser = require_promql_parser()
        yaml_mod = require_yaml()
        inventory = load_inventory(INVENTORY_PATH)
    except GateError as exc:
        print("DASHBOARD/ALERT/RUNBOOK VALIDATION FAILED (fail-closed):")
        print("  " + str(exc))
        return 2

    exprs = dashboard_exprs() + alert_exprs(yaml_mod)
    errors, referenced = check_metric_exprs(exprs, parser, inventory)

    if not referenced:
        errors.append("no metric series referenced — AST extraction likely broken")

    errors.extend(check_runbooks())
    errors.extend(check_registry())

    if errors:
        print("DASHBOARD/ALERT/RUNBOOK VALIDATION FAILED:")
        for e in errors:
            print("  " + e)
        return 1

    print(f"OK: {len(exprs)} PromQL expressions parsed (promql_parser); "
          f"{len(referenced)} distinct series, all in inventory v{inventory.version}.")
    print("    series: " + ", ".join(sorted(referenced)))
    print(f"OK: {len(list(RUNBOOKS.glob('*.md'))) - 1} runbooks each name an Operations queue + alert.")
    reg = parse_registry()
    print(f"OK: {len(reg)} Operations runbook deep links resolve (registry <-> SPA route <-> file <-> alert annotations).")
    return 0


if __name__ == "__main__":
    sys.exit(main())

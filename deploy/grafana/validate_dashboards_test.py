#!/usr/bin/env python3
"""Fail-closed negative fixtures for the §18/§20.1 observability gate (issue #156).

These prove the gate now FAILS where it previously passed silently:

* a required validator (promql_parser / PyYAML) that cannot be imported is a gate
  FAILURE, never a skip-to-success;
* invalid PromQL in a dashboard AND in an alert rule fails;
* a typo in EACH metric family (domain / RED / analytics / collector) fails,
  because the series is not in the single authoritative inventory;
* every selector is discovered regardless of metric prefix — an unknown series
  outside the historical prefix allowlist can no longer evade the check.

Runnable two ways (both exercised in CI so the deps are proven present, not
optional): ``uv run --with promql-parser pytest deploy/grafana`` and, mirroring
the sibling deploy tests, ``python3 deploy/grafana/validate_dashboards_test.py``.
"""

from __future__ import annotations

import pathlib
import sys

HERE = pathlib.Path(__file__).parent
sys.path.insert(0, str(HERE))

import validate_dashboards as v  # noqa: E402


# ── fail-closed on a missing required validator ──────────────────────────────
def test_missing_promql_parser_fails_closed() -> None:
    """A required parser that will not import is a gate FAILURE with an actionable
    message naming the missing dependency — never `parser = None; skip`."""
    original = v._import

    def boom(name: str) -> object:
        if name == "promql_parser":
            raise ImportError("simulated: promql_parser not installed")
        return original(name)

    v._import = boom
    try:
        raised = False
        try:
            v.require_promql_parser()
        except v.GateError as exc:
            raised = True
            assert "promql_parser" in str(exc)
            assert "promql-parser" in str(exc)  # names the pip artifact to install
        assert raised, "missing promql_parser must raise GateError, not return None"
    finally:
        v._import = original


def test_missing_yaml_fails_closed() -> None:
    original = v._import

    def boom(name: str) -> object:
        if name == "yaml":
            raise ImportError("simulated: PyYAML not installed")
        return original(name)

    v._import = boom
    try:
        raised = False
        try:
            v.require_yaml()
        except v.GateError as exc:
            raised = True
            assert "yaml" in str(exc).lower()
        assert raised, "missing PyYAML must raise GateError, not skip alert parsing"
    finally:
        v._import = original


# ── real parsing of every query AND every alert rule ─────────────────────────
def test_invalid_promql_in_dashboard_fails() -> None:
    parser = v.require_promql_parser()
    inv = v.load_inventory(v.INVENTORY_PATH)
    errors, _ = v.check_metric_exprs([("dk-x.json:Panel", "sum(rate(")], parser, inv)
    assert any("PARSE" in e for e in errors), errors


def test_invalid_promql_in_alert_rule_fails() -> None:
    parser = v.require_promql_parser()
    inv = v.load_inventory(v.INVENTORY_PATH)
    errors, _ = v.check_metric_exprs(
        [("dk-p0-alerts.yml:SomeAlert", "histogram_quantile(0.9, )")], parser, inv
    )
    assert any("PARSE" in e for e in errors), errors


# ── a typo in EACH family fails (inventory, not a permissive prefix) ─────────
def test_typo_in_each_family_fails() -> None:
    parser = v.require_promql_parser()
    inv = v.load_inventory(v.INVENTORY_PATH)
    typos = {
        "domain-execution": "execution_metric_that_does_not_exist",
        "domain-connector": "connector_metric_that_does_not_exist",
        "analytics": "analytics_metric_that_does_not_exist",
        "red-http": "http_server_request_duration_typo",
        "collector": "otelcol_metric_that_does_not_exist",
    }
    for family, name in typos.items():
        assert not inv.is_real(name), f"{family}: typo '{name}' must not be a real series"
        exprs = [(f"dk-{family}.json:P", f"sum({name})")]
        errors, referenced = v.check_metric_exprs(exprs, parser, inv)
        assert name in referenced, f"{family}: selector '{name}' must be discovered"
        assert any("SERIES" in e and name in e for e in errors), (family, errors)


def test_real_series_from_each_family_pass() -> None:
    inv = v.load_inventory(v.INVENTORY_PATH)
    for name in (
        "execution_write_attempts",
        "connector_sync_failure_streak",
        "analytics_events",
        "http_server_request_duration_bucket",
        "http_server_request_duration_count",
        "otelcol_exporter_sent_metric_points",
    ):
        assert inv.is_real(name), f"'{name}' should be an emitted/inventoried series"


# ── every selector discovered regardless of prefix ───────────────────────────
def test_odd_prefix_selector_is_discovered() -> None:
    """The historical bug: `metric_names` only looked at execution_/analytics_/
    http_/connector_/otelcol_ prefixes, so an unknown series outside them evaded
    the gate. AST-based discovery finds every selector."""
    parser = v.require_promql_parser()
    names = v.selector_names("max(weird_vendor_series) / scalar(another_odd_one)", parser)
    assert "weird_vendor_series" in names
    assert "another_odd_one" in names

    inv = v.load_inventory(v.INVENTORY_PATH)
    exprs = [("dk-x.json:P", "weird_vendor_series > 1")]
    errors, referenced = v.check_metric_exprs(exprs, parser, inv)
    assert "weird_vendor_series" in referenced
    assert any("SERIES" in e for e in errors), errors


def test_label_keys_are_not_mistaken_for_selectors() -> None:
    """`by (le, http_route)` and `{http_route="/x"}` are label keys, not metric
    selectors — AST discovery must not flag them (they'd fail the inventory)."""
    parser = v.require_promql_parser()
    expr = (
        "histogram_quantile(0.95, sum(rate("
        'http_server_request_duration_bucket{http_route="/x"}[5m])) by (le, http_route))'
    )
    names = v.selector_names(expr, parser)
    assert names == {"http_server_request_duration_bucket"}, names


# ── the inventory is the SINGLE source: no resurrected REAL_BASE / prefix regex ─
def test_no_handmaintained_real_base_or_prefix_allowlist() -> None:
    assert not hasattr(v, "REAL_BASE"), "REAL_BASE must be gone — inventory is the single source"
    assert not hasattr(v, "COLLECTOR_INTERNAL"), "the permissive ^otelcol_ prefix must be gone"


def _run_all() -> int:
    failures = 0
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            try:
                fn()
                print(f"PASS {name}")
            except Exception as exc:  # noqa: BLE001
                failures += 1
                print(f"FAIL {name}: {exc}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(_run_all())

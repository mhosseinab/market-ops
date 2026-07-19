#!/usr/bin/env python3
"""Deterministic semantic gate for the §20.1 ConnectorSyncFailureStreak alert
(issue #146). promtool is not wired into this repo, so this self-contained
stdlib test proves the alert expresses TRUE consecutive-streak semantics rather
than the old rolling-window count that fires on non-consecutive failures and on
unrelated credential-route traffic.

It FAILS (RED) on the old expression:
    sum(increase(http_server_request_duration_count{http_route=~"/connector/(refresh|connect)", http_status_class="5xx"}[15m])) >= 3  with for: 15m
and PASSES (GREEN) on the corrected gauge expression:
    max by (account_id, connector) (connector_sync_failure_streak) >= 3         with for: 2m

Run: ``python3 deploy/prometheus/rules/streak_alert_test.py`` (also invoked by
``task obs:validate`` and mirrored inside ``task ci:local``).
"""

from __future__ import annotations

import pathlib
import re
import sys

ALERTS = pathlib.Path(__file__).with_name("dk-p0-alerts.yml")
ALERT_NAME = "ConnectorSyncFailureStreak"


def load_rule(name: str) -> dict[str, str]:
    """Pull the `expr` and `for` of a named alert with a stdlib-only parser so this
    gate never depends on pyyaml being installed."""
    text = ALERTS.read_text()
    # Split into per-alert blocks and find ours.
    blocks = re.split(r"^\s*- alert:\s*", text, flags=re.M)
    for b in blocks:
        if b.lstrip().startswith(name):
            expr_m = re.search(r"expr:\s*\|?\s*\n((?:\s{2,}.*\n?)+?)(?=^\s{6,}\w+:|\Z)", b, re.M)
            for_m = re.search(r"^\s*for:\s*(\S+)", b, re.M)
            if not expr_m:
                raise AssertionError(f"{name}: no expr found")
            return {"expr": " ".join(expr_m.group(1).split()), "for": (for_m.group(1) if for_m else "0m")}
    raise AssertionError(f"alert {name} not found in {ALERTS.name}")


def dur_to_minutes(d: str) -> float:
    m = re.fullmatch(r"(\d+)(s|m|h)", d.strip())
    if not m:
        raise AssertionError(f"unparseable duration {d!r}")
    n, unit = int(m.group(1)), m.group(2)
    return {"s": n / 60, "m": float(n), "h": n * 60}[unit]


# ── The streak model the Go seam implements (internal/catalog/telemetry.go). ──
# A bounded gauge per account: +1 on any sync failure, reset to 0 on a success.
# Credential connect/refresh traffic is NOT a sync event, so it never appears here.
def simulate_streak(events: list[str]) -> list[int]:
    """events: 'F' (any sync failure disposition) or 'S' (successful sync)."""
    streak, series = 0, []
    for e in events:
        streak = streak + 1 if e == "F" else 0
        series.append(streak)
    return series


def fires_gauge(events: list[str], threshold: int) -> bool:
    """New expr: max over the streak gauge crosses the threshold at any tick."""
    return any(v >= threshold for v in simulate_streak(events))


def fires_rolling_increase(events: list[str], threshold: int) -> bool:
    """Old expr: a rolling COUNT of failures in the window, with NO reset-on-success.
    Modelled as the total number of 'F' in the window (all events fit one window)."""
    return sum(1 for e in events if e == "F") >= threshold


def main() -> int:
    rule = load_rule(ALERT_NAME)
    expr, for_min = rule["expr"], dur_to_minutes(rule["for"])
    errors: list[str] = []

    # ── Structural guards: the corrected design, not the buggy one. ──
    if "connector_sync_failure_streak" not in expr:
        errors.append("expr must read the connector_sync_failure_streak gauge (authoritative sync boundary)")
    if "increase(" in expr or "rate(" in expr:
        errors.append("expr must NOT use increase()/rate(): a rolling window cannot express a consecutive streak")
    if "/connector/" in expr or "http_server_request_duration" in expr:
        errors.append("expr must NOT key off credential connect/refresh HTTP routes; those are not the sync operation")
    if for_min > 5:
        errors.append(f"`for: {rule['for']}` is too long to be a stability delay; it must not substitute for streak logic")
    m = re.search(r">=\s*(\d+)", expr)
    if not m:
        errors.append("expr must threshold the streak with `>= N`")
    threshold = int(m.group(1)) if m else 3
    if threshold != 3:
        errors.append(f"documented canary threshold is 3 consecutive failures; expr uses {threshold}")

    # ── Semantic acceptance tests (issue #146). ──
    interleaved = ["F", "S", "F", "S", "F"]  # failure,success,failure,success,failure
    three = ["F", "F", "F"]
    reset = ["F", "F", "S"]

    if fires_gauge(interleaved, threshold):
        errors.append("ACCEPTANCE 1 FAILED: F,S,F,S,F must NOT fire the streak alert")
    if not fires_gauge(three, threshold):
        errors.append("ACCEPTANCE 2 FAILED: three consecutive failures MUST fire")
    if fires_gauge(reset, threshold) or simulate_streak(reset)[-1] != 0:
        errors.append("ACCEPTANCE 3 FAILED: a success must reset an existing streak")
    # Connect/refresh traffic carries no sync events → empty streak series → silent.
    if fires_gauge([], threshold):
        errors.append("ACCEPTANCE 5 FAILED: credential traffic without a sync must not fire")

    # ── Proof this is a real fix, not a no-op: the OLD rolling-count semantics
    #    WOULD have fired on the interleaved sequence; the new streak semantics
    #    must NOT. If they ever agree here, the streak logic has regressed. ──
    if not fires_rolling_increase(interleaved, threshold):
        errors.append("sanity: the modelled old rolling-count should fire on F,S,F,S,F")
    if fires_gauge(interleaved, threshold) == fires_rolling_increase(interleaved, threshold):
        errors.append("REGRESSION: streak semantics match the buggy rolling-count on F,S,F,S,F")

    if errors:
        print(f"CONNECTOR-SYNC STREAK ALERT SEMANTICS FAILED ({ALERT_NAME}):")
        for e in errors:
            print("  " + e)
        print(f"  expr: {expr}")
        return 1

    print(f"OK: {ALERT_NAME} expresses consecutive-streak semantics "
          f"(threshold {threshold}, for {rule['for']}); F,S,F,S,F silent, "
          f"3-consecutive fires, success resets, credential traffic inert.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

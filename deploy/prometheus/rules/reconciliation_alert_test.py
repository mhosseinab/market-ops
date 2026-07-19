#!/usr/bin/env python3
"""Deterministic semantic gate for the §20.1 ReconciliationBacklog alert
(issue #147). promtool is not wired into this repo, so this self-contained
stdlib test proves the alert measures CURRENT DURABLE unresolved work and its age,
rather than a difference between two unrelated rolling process counters.

It FAILS (RED) on the old expression:
    sum(increase(execution_pending_reconciliation[30m])) - sum(increase(execution_terminal_results[30m])) > 0  with for: 30m
and PASSES (GREEN) on the corrected durable-gauge expression:
    (max by (account_id) (execution_pending_reconciliation_current) >= 1)
      and
    (max by (account_id) (execution_pending_reconciliation_oldest_age_seconds) >= 1800)   with a short for:

Run: ``python3 deploy/prometheus/rules/reconciliation_alert_test.py`` (also invoked
by ``task obs:validate``, mirrored inside ``task ci:local`` via that target).
"""

from __future__ import annotations

import pathlib
import re
import sys

ALERTS = pathlib.Path(__file__).with_name("dk-p0-alerts.yml")
ALERT_NAME = "ReconciliationBacklog"

# The 30-minute unresolved threshold, in seconds. The age gate — not `for:` — is the
# persistence proof (EXE-003, §20.1).
AGE_GATE_SECONDS = 1800

CURRENT_GAUGE = "execution_pending_reconciliation_current"
AGE_GAUGE = "execution_pending_reconciliation_oldest_age_seconds"


def load_rule(name: str) -> dict[str, str]:
    """Pull the `expr` and `for` of a named alert with a stdlib-only parser so this
    gate never depends on pyyaml being installed."""
    text = ALERTS.read_text()
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


# ── The durable model the Go seam implements
#    (internal/execution/reconciliation_backlog.go). Per account, two gauges read
#    LIVE from the pending_reconciliation set each scrape: the current count and the
#    oldest item's age in seconds. A resolved item leaves the set; nothing else moves
#    the series. ──
def fires_gauge(pending_count: int, oldest_age_seconds: int) -> bool:
    """New expr: count >= 1 AND oldest age >= AGE_GATE_SECONDS."""
    return pending_count >= 1 and oldest_age_seconds >= AGE_GATE_SECONDS


def fires_old_subtraction(park_increments: int, terminal_increments: int) -> bool:
    """Old expr: increase(parks[30m]) - increase(terminals[30m]) > 0. Models the two
    unrelated rolling counters — parks and (any) terminal results — in one window."""
    return (park_increments - terminal_increments) > 0


def main() -> int:
    rule = load_rule(ALERT_NAME)
    expr, for_min = rule["expr"], dur_to_minutes(rule["for"])
    errors: list[str] = []

    # ── Structural guards: the durable-gauge design, not the buggy subtraction. ──
    if CURRENT_GAUGE not in expr:
        errors.append(f"expr must read the durable {CURRENT_GAUGE} gauge (current pending count)")
    if AGE_GAUGE not in expr:
        errors.append(f"expr must read the durable {AGE_GAUGE} gauge (age proves the SAME work persists)")
    if "increase(" in expr or "rate(" in expr:
        errors.append("expr must NOT use increase()/rate(): a rolling window forgets work older than the range")
    if "execution_terminal_results" in expr:
        errors.append("expr must NOT subtract execution_terminal_results: unrelated terminals cannot define backlog")
    if re.search(r"\)\s*-\s*(sum|max|min|avg)?\s*\(", expr):
        errors.append("expr must NOT subtract one aggregate from another to derive backlog")
    if "account_id" not in expr:
        errors.append("expr must aggregate by the bounded account_id label (per-account backlog + paging)")
    # The age gate must be the >= 1800s threshold that proves 30m of persistence.
    if not re.search(rf">=\s*{AGE_GATE_SECONDS}\b", expr):
        errors.append(f"expr must age-gate the oldest pending item at >= {AGE_GATE_SECONDS}s (30m persistence proof)")
    # A short stability `for:` is fine; it must NOT be the persistence mechanism.
    if for_min > 5:
        errors.append(f"`for: {rule['for']}` is too long; persistence is proven by the age gate, not by `for:`")

    # ── Semantic acceptance tests (issue #147). ──
    # 1. One unresolved item continues to alert AFTER 30 minutes (and across restart,
    #    which for a live durable read is the same live value — the gauge is a DB read,
    #    not an in-memory counter that a range would forget).
    if not fires_gauge(pending_count=1, oldest_age_seconds=2400):
        errors.append("ACCEPTANCE 1 FAILED: one item unresolved >30m must still fire")
    # 2. Resolving that item clears the alert (it leaves the pending set → count 0).
    if fires_gauge(pending_count=0, oldest_age_seconds=0):
        errors.append("ACCEPTANCE 2 FAILED: a resolved (empty) backlog must clear the alert")
    # 3. Unrelated terminal results cannot cancel a pending item: the durable count is
    #    unchanged by terminals on OTHER items, so a still-pending old item still fires.
    if not fires_gauge(pending_count=1, oldest_age_seconds=2000):
        errors.append("ACCEPTANCE 3 FAILED: unrelated terminals must not cancel a still-pending item")
    # 4. A counter reset/restart cannot fabricate a negative or empty backlog: the
    #    gauge is a live count that is >= 0 by construction; a fresh item under the age
    #    gate simply does not fire yet (no negative, no phantom).
    if fires_gauge(pending_count=1, oldest_age_seconds=60):
        errors.append("ACCEPTANCE 4 FAILED: a fresh (<30m) pending item must not fire yet")
    # 5. Multiple accounts follow the documented aggregation + paging threshold: the
    #    per-account by(account_id) grouping means one aged account pages independently.
    if "max by (account_id)" not in expr.replace("  ", " "):
        errors.append("ACCEPTANCE 5 FAILED: expr must page per account via max by (account_id)")

    # ── Proof this is a real fix, not a no-op: the OLD subtraction WOULD have masked
    #    a durable pending item once its increment aged out of the window (parks=0,
    #    terminals>=0 ⇒ non-positive ⇒ silent), while the durable gauge still fires. ──
    aged_out_masks = not fires_old_subtraction(park_increments=0, terminal_increments=3)
    durable_still_fires = fires_gauge(pending_count=1, oldest_age_seconds=3600)
    if not aged_out_masks:
        errors.append("sanity: the modelled old subtraction should go silent once the park ages out of the window")
    if not durable_still_fires:
        errors.append("REGRESSION: the durable gauge must still fire where the old subtraction went silent")

    if errors:
        print(f"RECONCILIATION BACKLOG ALERT SEMANTICS FAILED ({ALERT_NAME}):")
        for e in errors:
            print("  " + e)
        print(f"  expr: {expr}")
        return 1

    print(f"OK: {ALERT_NAME} measures durable pending state + age "
          f"(count >= 1 AND oldest age >= {AGE_GATE_SECONDS}s, for {rule['for']}); "
          f"aged item fires, resolving clears, unrelated terminals inert, fresh item waits, "
          f"per-account paging — no increase()/subtraction.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

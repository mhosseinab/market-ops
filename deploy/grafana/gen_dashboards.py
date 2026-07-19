#!/usr/bin/env python3
"""Generate the §18 Grafana dashboard set (plus the §17.2 SLO/RED overview).

This is the SINGLE SOURCE for the dashboard JSON under deploy/grafana/dashboards.
Editing a panel means editing this file and re-running it (``task obs:dashboards``
or ``python3 deploy/grafana/gen_dashboards.py``), then committing the JSON. A
generator keeps every panel's PromQL bound to a REAL emitted series (the metric
names the S18 execution telemetry, S19 analytics/cost pipe, and S33 gateway RED
seam actually emit through the collector's prometheus exporter with
add_metric_suffixes:false, i.e. OTLP names with `.`→`_`). Beta-data panels may
render empty, but every target queries a series that exists.

Message count and conversation length are §18 ANTI-metrics — no panel here counts
them; the chat dashboard measures latency, cost, grounding, and containment only.
"""

from __future__ import annotations

import json
import pathlib

OUT_DIR = pathlib.Path(__file__).parent / "dashboards"
PROM = {"type": "prometheus", "uid": "prometheus"}

_panel_id = 0


def _next_id() -> int:
    global _panel_id
    _panel_id += 1
    return _panel_id


def target(expr: str, legend: str = "") -> dict:
    return {
        "datasource": PROM,
        "expr": expr,
        "legendFormat": legend,
        "refId": chr(65 + (_next_id() % 26)),
        "range": True,
    }


def timeseries(title: str, targets: list[dict], desc: str = "", unit: str = "short", x: int = 0, y: int = 0, w: int = 12, h: int = 8) -> dict:
    return {
        "id": _next_id(),
        "type": "timeseries",
        "title": title,
        "description": desc,
        "datasource": PROM,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit, "custom": {"drawStyle": "line", "fillOpacity": 10}}, "overrides": []},
        "options": {"legend": {"displayMode": "table", "placement": "bottom"}, "tooltip": {"mode": "multi"}},
        "targets": targets,
    }


def stat(title: str, targets: list[dict], desc: str = "", unit: str = "short", x: int = 0, y: int = 0, w: int = 6, h: int = 6) -> dict:
    return {
        "id": _next_id(),
        "type": "stat",
        "title": title,
        "description": desc,
        "datasource": PROM,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit}, "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": "value"},
        "targets": targets,
    }


def dashboard(uid: str, title: str, tags: list[str], panels: list[dict], desc: str = "") -> dict:
    return {
        "uid": uid,
        "title": title,
        "description": desc,
        "tags": ["dk-p0", *tags],
        "timezone": "utc",
        "schemaVersion": 39,
        "version": 1,
        "editable": True,
        "refresh": "30s",
        "time": {"from": "now-6h", "to": "now"},
        "templating": {"list": []},
        "panels": panels,
    }


# P95 latency over a route from the RED histogram (real series, S33 gateway seam).
def p95(route_regex: str) -> str:
    return (
        "histogram_quantile(0.95, sum by (le, http_route) "
        f'(rate(http_server_request_duration_bucket{{http_route=~"{route_regex}"}}[5m])))'
    )


def build() -> list[dict]:
    dashboards: list[dict] = []

    # 1. Activation and first value
    dashboards.append(dashboard(
        "dk-activation", "DK · Activation & first value", ["activation"],
        [
            timeseries("Connection & capability lifecycle", [target('sum by (name) (rate(analytics_events{family="connection"}[5m]))', "{{name}}")],
                       "§18 connection/capability lifecycle events (analytics_events, family=connection).", x=0, y=0),
            timeseries("Sync & import lifecycle", [target('sum by (name) (rate(analytics_events{family="sync"}[5m]))', "{{name}}")],
                       "§18 sync/import lifecycle events.", x=12, y=0),
            timeseries("First-view P95 (§17.2 common views < 2s)", [target(p95("/today|/market|/events"), "p95")],
                       "P95 of the common product views. §17.2 target: < 2s.", unit="ms", x=0, y=8),
        ],
        "First-value funnel: connection → sync → first product view (§17.2 < 2s).",
    ))

    # 2. WVRA by execution mode
    dashboards.append(dashboard(
        "dk-wvra", "DK · WVRA by execution mode", ["wvra"],
        [
            timeseries("Execution-family lifecycle (by name)", [target('sum by (name) (rate(analytics_events{family="execution"}[1h]))', "{{name}}")],
                       "Execution/reconciliation/recommend-only/outcome events (§18).", x=0, y=0),
            timeseries("Recommend-only vs terminal writes", [
                target("sum(rate(execution_recommend_only[1h]))", "recommend_only"),
                target("sum by (external_state) (rate(execution_terminal_results[1h]))", "terminal {{external_state}}"),
            ], "WVRA denominator/numerator: recommend-only matches vs reconciled terminal writes (S18 counters).", x=12, y=0),
        ],
        "Weekly Verified Recommended Action split by execution mode (write vs recommend-only).",
    ))

    # 3. Identity and money-unit quality
    dashboards.append(dashboard(
        "dk-identity-money", "DK · Identity & money-unit quality", ["identity", "money"],
        [
            timeseries("Mapping decisions (by name)", [target('sum by (name) (rate(analytics_events{family="mapping"}[1h]))', "{{name}}")],
                       "Identity mapping decisions (§18 mapping family).", x=0, y=0),
            timeseries("Write enablement denied (capability/region gate)", [target("sum(rate(execution_enablement_denied[1h]))", "enablement_denied")],
                       "Capability/region gate denials — money/identity quarantine keeps writes OFF until verified (S18).", x=12, y=0),
            stat("Audit-write failures (must be 0)", [target("sum(increase(execution_audit_write_failures[24h]))", "audit_failures")],
                 "Never-cut: an audit loss is an incident. Any non-zero value is a fail-closed rollback that needs investigation.", x=0, y=8),
        ],
        "Identity mapping + money-unit gating; quarantine-over-inference is visible, not silent.",
    ))

    # 4. Observation quality / freshness / route cost
    dashboards.append(dashboard(
        "dk-observation", "DK · Observation quality, freshness & route cost", ["observation"],
        [
            timeseries("Observation events (capture/quality/freshness/drift)", [target('sum by (name) (rate(analytics_events{family="observation"}[15m]))', "{{name}}")],
                       "§18 observation family: capture, quality, freshness, drift, route budget.", x=0, y=0),
            timeseries("Route/observation variable cost", [
                target('sum(increase(analytics_cost_minor_units{cost_kind="successful_fresh_observation"}[1h]))', "fresh_observation"),
                target('sum(increase(analytics_cost_minor_units{cost_kind="target"}[1h]))', "target"),
            ], "§17.3 observation/route cost (minor units). Observation budgets reduce targets before widening freshness.", unit="none", x=12, y=0),
        ],
        "Observation capture quality, freshness tiers, and route budget cost.",
    ))

    # 5. Event precision and noise
    dashboards.append(dashboard(
        "dk-events", "DK · Event precision & noise", ["events"],
        [
            timeseries("Event lifecycle & relevance (by name)", [target('sum by (name) (rate(analytics_events{family="event"}[1h]))', "{{name}}")],
                       "§18 event family: lifecycle + relevance feedback. Precision = relevant / total.", x=0, y=0),
        ],
        "Event precision and noise from the event-family analytics stream.",
    ))

    # 6. Recommendation coverage and blockers
    dashboards.append(dashboard(
        "dk-recommendations", "DK · Recommendation coverage & blockers", ["recommendations"],
        [
            timeseries("Recommendation & simulation events (by name)", [target('sum by (name) (rate(analytics_events{family="recommendation"}[1h]))', "{{name}}")],
                       "§18 recommendation/simulation family.", x=0, y=0),
            timeseries("Execution gate blocks (blockers, by gate)", [target("sum by (gate) (rate(execution_gate_blocks[1h]))", "{{gate}}")],
                       "EXE-001 revalidation gate blocks by gate — the coverage blockers (S18).", x=12, y=0),
        ],
        "Recommendation coverage and the gate blockers that suppress an action.",
    ))

    # 7. Approval / execution integrity
    dashboards.append(dashboard(
        "dk-approval-execution", "DK · Approval & execution integrity", ["approval", "execution"],
        [
            timeseries("External write attempts (by state)", [target("sum by (external_state) (rate(execution_write_attempts[5m]))", "{{external_state}}")],
                       "EXE-002 external price-write attempts by result state (S18).", x=0, y=0),
            timeseries("Idempotency dedup hits", [target("sum(rate(execution_dedup_hits[5m]))", "dedup_hits")],
                       "Duplicate requests that wrote nothing — idempotency working (never-cut).", x=12, y=0),
            stat("Audit-write failures (never-cut, must be 0)", [target("sum(increase(execution_audit_write_failures[24h]))", "audit_failures")],
                 "Any non-zero value = an audit append that forced a rollback. Fail-closed, page-worthy.", x=0, y=8),
            stat("Pending reconciliation (30m)", [target("sum(increase(execution_pending_reconciliation[30m]))", "pending")],
                 "EXE-003 unknown write results parked pending reconciliation.", x=6, y=8),
            timeseries("Approval-card P95 (§17.2 < 5s, no model dep)", [target(p95("/approvals/card"), "p95")],
                       "Approval card latency. §17.2 target: < 5s without model dependency.", unit="ms", x=12, y=8),
        ],
        "The never-cut approval/execution boundaries: write attempts, dedup, audit integrity, reconciliation, card latency.",
    ))

    # 8. Chat adoption / context / grounding / latency / cost / containment
    dashboards.append(dashboard(
        "dk-chat", "DK · Chat adoption, grounding, latency, cost & containment", ["chat"],
        [
            timeseries("Chat completion P95 (§17.2 read-only < 10s)", [target(p95("/chat"), "p95_completion")],
                       "Chat turn latency. §17.2: first token < 3s, read-only completion < 10s. NOTE: message count / conversation length are §18 ANTI-metrics and are never charted.", unit="ms", x=0, y=0),
            timeseries("Conversation events (context/grounding/tool/deep-link)", [target('sum by (name) (rate(analytics_events{family="conversation"}[15m]))', "{{name}}")],
                       "§18 conversation family: intent, context, tool, grounding, deep-link. Optimizes for shortest safe decision path.", x=12, y=0),
            timeseries("Chat variable cost (§17.3 conversation)", [target('sum(increase(analytics_cost_minor_units{cost_kind="conversation"}[1h]))', "conversation_cost")],
                       "§17.3 per-conversation variable cost (minor units). Budget pressure triggers the degradation ladder.", unit="none", x=0, y=8),
            timeseries("Free-text containment (guidance-only routing)", [target('sum by (name) (rate(analytics_events{family="conversation", name=~".*contain.*|.*guidance.*|.*blocked.*"}[1h]))', "{{name}}")],
                       "Free-text approve/execute attempts routed to guidance-only (§8 containment). Distinguishes a contained attempt from a silent bypass.", x=12, y=8),
        ],
        "Chat health: latency vs §17.2, grounding/context, cost vs §17.3, and free-text containment. No anti-metrics.",
    ))

    # 9. Unit economics
    dashboards.append(dashboard(
        "dk-unit-economics", "DK · Unit economics", ["economics"],
        [
            timeseries("Variable cost by kind (§17.3)", [target("sum by (cost_kind) (increase(analytics_cost_minor_units[1h]))", "{{cost_kind}}")],
                       "Every §17.3 variable-cost counter: account, managed SKU, target, fresh observation, briefing, conversation, simulation, approval flow, execution attempt.", unit="none", x=0, y=0),
            timeseries("Variable cost by source surface (24h)", [target("sum by (source_surface) (increase(analytics_cost_minor_units[24h]))", "{{source_surface}}")],
                       "Daily variable cost by source surface (bounded label). Per-ACCOUNT cost drill-down is intentionally NOT a Prometheus label (tenant UUID = unbounded/tenant-sensitive cardinality, issue #151); daily model-spend budget attribution per account lives in the authorized/persisted analytics query plane, not metrics.", unit="none", x=12, y=0),
        ],
        "Unit economics from §17.3 granular cost counters.",
    ))

    # 10. Outcomes and confidence
    dashboards.append(dashboard(
        "dk-outcomes", "DK · Outcomes & confidence", ["outcomes"],
        [
            timeseries("Reconciled terminal results (by state)", [target("sum by (external_state) (rate(execution_terminal_results[1h]))", "{{external_state}}")],
                       "OUT-001 reconciled terminal external results by state (S18).", x=0, y=0),
            timeseries("Outcome events", [target('sum by (name) (rate(analytics_events{family="execution", name=~".*outcome.*|.*confidence.*"}[6h]))', "{{name}}")],
                       "§18 outcome/confidence events on the execution family.", x=12, y=0),
        ],
        "Outcome windows and confidence after reconciled execution.",
    ))

    # 11. SLO / RED overview (§17.2 performance & reliability)
    dashboards.append(dashboard(
        "dk-slo-red", "DK · SLO / RED overview (§17.2)", ["slo", "red"],
        [
            timeseries("Request rate by route", [target("sum by (http_route) (rate(http_server_request_duration_count[5m]))", "{{http_route}}")],
                       "RED · Rate: gateway requests per route (S33 gateway seam).", unit="reqps", x=0, y=0),
            timeseries("Error rate by class", [target("sum by (http_status_class) (rate(http_server_request_duration_count{http_status_class=~\"4xx|5xx\"}[5m]))", "{{http_status_class}}")],
                       "RED · Errors: 4xx/5xx rate.", unit="reqps", x=12, y=0),
            timeseries("P95 latency by route (§17.2)", [target("histogram_quantile(0.95, sum by (le, http_route) (rate(http_server_request_duration_bucket[5m])))", "{{http_route}}")],
                       "RED · Duration: P95 per route against §17.2 targets (views < 2s, approval card < 5s, chat < 10s).", unit="ms", x=0, y=8),
            stat("Collector: OTLP metric points exported", [target("sum(rate(otelcol_exporter_sent_metric_points[5m]))", "exported")],
                 "Collection-layer health: metric points leaving the collector (docs/14). Zero ⇒ instrumentation or collector broken.", x=12, y=8),
        ],
        "§17.2 performance & reliability: RED per endpoint + P95 targets + collection-layer health.",
    ))

    return dashboards


def main() -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for dash in build():
        path = OUT_DIR / f"{dash['uid']}.json"
        path.write_text(json.dumps(dash, indent=2, ensure_ascii=False) + "\n")
        print(f"wrote {path.relative_to(pathlib.Path(__file__).parent.parent.parent)}")


if __name__ == "__main__":
    main()

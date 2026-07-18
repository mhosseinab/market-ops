# Runbook — Connector / sync failure

**Failure domain:** DK Seller connector (ACC-001, sync/import lifecycle).
**Owning Operations queue (OPS-002):** `operations.queue.failedSync`
(Operations screen → "Failed sync"; web deep link `/docs/runbooks/connector-sync`).
**Alert:** `ConnectorSyncFailureStreak` (`deploy/prometheus/rules/dk-p0-alerts.yml`).
**Dashboards:** `DK · Activation & first value`, `DK · SLO / RED overview (§17.2)`.

## Symptom

- Alert `ConnectorSyncFailureStreak` firing: ≥3 5xx on `/connector/refresh` or
  `/connector/connect` in 15m (docs/14: three consecutive non-200 canary responses).
- Operations → "Failed sync" queue count ≥ 1 (connection state ≠ `connected`).
- Initial import missing the §17.2 target (95% within 4h for 5,000 SKUs) or
  incremental sync exceeding P95 15m.

## Owning queue and ownership boundary

This is platform-observed but the connector's own resilience (backoff, capability
transitions) is owned by `go_connector_observer`. Platform owns the alert, the
queue mapping, and the telemetry landing zone — not the connector business logic.

## Diagnosis

1. Confirm scope on `DK · SLO / RED overview`: is the error rate isolated to the
   `/connector/*` routes, or is the whole gateway degraded? Whole-gateway ⇒ treat
   as a core incident, not a connector one.
2. Check the connection state: Operations → "Failed sync" shows non-`connected`.
   A quarantined identity is never retried past its window (never-cut) — verify the
   failure is a sync error, not an intentional quarantine.
3. Inspect structured logs in Loki for the connector component (JSON keys only; no
   token, no PII). Correlate by the trace id from Tempo (the RED span carries the
   route). Look for auth expiry, DK 5xx, or backpressure signals.
4. Confirm the DK Seller API is reachable (the mock in dev; live only under an
   explicit human "go"). A DK-side outage is an upstream event, not a code bug —
   it consumes error budget as a quarantine, not a defect.

## Recovery

1. **Transient DK/auth error:** connector retries under its own backoff. Confirm
   the streak clears on the RED error panel; the queue count returns to 0.
2. **Expired/invalid credential:** re-run the connector connect flow (Operations →
   "Failed sync" → Open queue → onboarding). Capabilities re-probe to Unknown and
   re-resolve — no dependent UI enables until a capability is Supported.
3. **Sustained DK outage:** leave the account visibly recommend-only; do not force
   writes. Record the outage window so §19.4 reconciled-success excludes it.
4. **Backpressure engaged:** this is expected load-shedding, observed not silent.
   Advisory UI sheds before observations before reconciliation before audit/approval
   (never shed audit/approval). Confirm the shed order in logs.

## Exit

Alert resolved, "Failed sync" queue at 0, sync P95 back within §17.2, and the
connection-lifecycle panel on `DK · Activation & first value` shows resumed events.

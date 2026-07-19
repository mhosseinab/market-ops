# Runbook — Route C parser drift

**Failure domain:** Route C parser / normalization (`internal/routec`, §10.4).
**Owning Operations queue (OPS-002):** `operations.queue.parserDrift`
(Operations screen → "Parser / schema drift"; web deep link `/operations/runbooks/parser-drift`).
**Alert:** `RouteCircuitOpen` (`deploy/prometheus/rules/dk-p0-alerts.yml`).
**Dashboards:** `DK · Observation quality, freshness & route cost`,
`DK · SLO / RED overview (§17.2)`.

## Symptom

- Alert `RouteCircuitOpen` firing: a Route B/C or extension circuit stop event
  (§18 observation/extension family) — drift paused dependent extraction.
- Operations → "Parser / schema drift" queue surfaced (its backing list endpoint
  is not yet in the P0 contract, so it shows an explicit unavailable count, never a
  fabricated zero — carry-forward for `api_data_contracts`).
- docs/14 signals: extraction success dropping per page type, missing critical
  fields, response key-set drift, or three consecutive canary non-200 responses.

## Owning queue and ownership boundary

Parser correctness and the Route C circuit breaker are owned by
`go_connector_observer`. Platform owns the drift alert, the queue mapping, and the
telemetry. A parser drift is a §10.4 event and consumes error budget as an upstream
DK change, not a code defect — unless a code change caused it.

## Diagnosis

1. Confirm the drift class on the observation dashboard: selector failure vs
   response key-set drift vs value/unit distribution shift.
2. Verify affected values were marked `Unavailable` or `Stale` (not silently
   coerced). A drifted value that still reads as fresh/valid is a never-cut bug.
3. Confirm parser version and evidence remain attached to every affected
   observation (append-only; no UPDATE).
4. Roll metrics up by `connectorVersion` / `schemaVersion` (docs/14) to see whether
   a release regression or an upstream DK page change caused the drift.

## §10.4 Drift and recovery procedure (PRD, verbatim)

### 10.4 Drift and recovery

- Parser releases require golden fixtures.
- Live canary checks required fields and value/unit distributions.
- Drift pauses dependent extraction and marks affected values Unavailable or Stale.
- Recovery requires a green fixture set, a green canary, and a manual sample.
- Parser version and evidence remain attached to every observation.

## Recovery (applying §10.4)

1. Keep dependent extraction paused; affected values stay `Unavailable`/`Stale`.
2. Reproduce the drift against the golden fixtures
   (`docs/DK-public-research-result/06-dom-and-selector-contract.md`). If reality
   drifted from the documented selectors/endpoints, that is a parser-drift event,
   not a silent code change.
3. Ship the corrected parser only with a **green fixture set**.
4. Run the **live canary**; it must pass required-field and value/unit checks.
5. Take a **manual sample** and confirm it matches.
6. Only after green fixtures + green canary + manual sample, resume dependent
   extraction. The circuit closes; confirm fresh observations resume and the alert
   clears.

## Exit

Green fixtures, green canary, manual sample confirmed, circuit closed, alert
resolved, and no values left silently coerced.

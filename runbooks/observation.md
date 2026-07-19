# Runbook — Observation quality / freshness / route budget

**Failure domain:** Observation pipeline (§7.3, §10; capture, quality, freshness,
route budget).
**Owning Operations queue (OPS-002):** `operations.queue.staleTargets`
(Operations screen → "Stale targets"; web deep link `/operations/runbooks/observation-freshness`),
with `operations.queue.conflicted` (deep link `/operations/runbooks/observation-conflict`)
for conflicted evidence and `operations.queue.identityMapping` (deep link
`/operations/runbooks/identity-mapping`) for identity-quarantine mapping review —
this is the shared observation-domain runbook for all three queues (issue #159).
**Alert:** `RouteCircuitOpen` (route budget / breaker trip →
`deploy/prometheus/rules/dk-p0-alerts.yml`).
**Dashboards:** `DK · Observation quality, freshness & route cost`.

## Symptom

- Operations → "Stale targets" count rising (offers older than the freshness
  window) and/or "Conflicted" count rising.
- Alert `RouteCircuitOpen` firing (a Route C / extension breaker tripped, cutting
  fresh observations).
- Observation cost climbing on `DK · Observation quality…` without matching fresh
  observations — route budget pressure.

## Owning queue and ownership boundary

Route C circuit breakers, backoff, and kill switches are owned by
`go_connector_observer`; the extension's queue/batch/retry is owned by
`chrome_extension`. Platform owns the freshness/cost/quality dashboards, the
alert, and the queue mapping.

## Diagnosis

1. On `DK · Observation quality…`, separate the three signals: capture quality
   (drift/selector failures), freshness (stale age per tier), and route cost.
2. **Stale but no breaker:** the scheduler is behind. Per §17.3, observation
   budgets reduce scheduled targets *before* widening the freshness window — confirm
   the budget ladder engaged, not a silent freshness relaxation (OPS-001: a context
   signal never silently changes policy).
3. **Conflicted evidence:** conflicting observations are quarantined with evidence,
   never coerced (quarantine over inference). Confirm each conflicted offer carries
   its evidence and quality state; a silently-dropped conflict is a bug.
4. **Breaker open:** hand off to the parser runbook if the trip is a parser-drift
   event (§10.4); otherwise it is a block-rate/route-health trip owned by the
   observer. A breaker trip must have emitted an audited event — confirm it did; a
   silent trip is a bug.

## Recovery

1. **Scheduler behind:** verify the budget-driven target reduction; let the priority
   tier catch up first. Do not widen freshness to hide the backlog.
2. **Route budget exhausted:** the observer reduces scheduled targets. Confirm cost
   stops climbing on the dashboard; stale count drains as budget frees.
3. **Conflicted spike:** route conflicted offers to the "Conflicted" queue for
   manual identity/evidence review; they never auto-resolve.
4. **Breaker open:** follow `parser.md` recovery if drift; otherwise wait for the
   observer's half-open probe to close the breaker, then confirm fresh observations
   resume.

## Exit

Stale/conflicted queues draining, route cost flat against fresh-observation volume,
no open breaker, and the freshness panel back within tier targets.

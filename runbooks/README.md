# Operations runbooks (PRD §20.1)

One runbook per §20.1 failure domain. Each is `symptom → owning Operations queue
→ alert → diagnosis → recovery`, and every runbook names the exact Operations
queue (OPS-002) that owns the blocked journey and the Prometheus alert that trips.

| Runbook | Failure domain | Operations queue (OPS-002) | Web deep link (S28) | Alert (deploy/prometheus/rules) |
|---|---|---|---|---|
| [connector.md](connector.md) | Connector / sync | `operations.queue.failedSync` | `/docs/runbooks/connector-sync` | `ConnectorSyncFailureStreak` |
| [observation.md](observation.md) | Observation quality/freshness | `operations.queue.staleTargets` (+ `operations.queue.conflicted`) | `/docs/runbooks/observation-freshness` | `RouteCircuitOpen` |
| [parser.md](parser.md) | Route C parser drift | `operations.queue.parserDrift` | `/docs/runbooks/parser-drift` | `RouteCircuitOpen` |
| [action-reconciliation.md](action-reconciliation.md) | Action reconciliation | `operations.queue.pendingRecon` | `/docs/runbooks/reconciliation` | `ReconciliationBacklog` |
| [llm-outage.md](llm-outage.md) | LLM / chat / briefing outage | *(no blocking queue by design)* — triage via `operations.queue.staleTargets` | — | `BriefingGenerationFailure`, `ModelSpendBudgetExhausted` |

The queue keys are the locale catalog keys the Operations screen renders
(`apps/web/src/screens/Operations.tsx`); the web deep links are the `RUNBOOK` map
in that screen. The alerts live in `deploy/prometheus/rules/dk-p0-alerts.yml`,
each carrying a `runbook` and `ops_queue` annotation that points back here.

The dashboards referenced below live under `deploy/grafana/dashboards/` and are
listed in the DK P0 Grafana folder.

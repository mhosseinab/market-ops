# Operations runbooks (PRD §20.1)

One runbook per §20.1 failure domain. Each is `symptom → owning Operations queue
→ alert → diagnosis → recovery`, and every runbook names the exact Operations
queue (OPS-002) that owns the blocked journey and the Prometheus alert that trips.

The web deep link is an **in-SPA** route (OPS-002 / issue #159): the Operations
screen links each queue to `/operations/runbooks/<slug>`, a viewer registered in
the SPA router that renders the backing runbook file behind the same Internal-role
gate as Operations. The `<slug>` is a technical identifier from the canonical
registry `apps/web/src/app/runbooks.ts` — the single source of truth the screen
links, the viewer, and `deploy/grafana/validate_dashboards.py` all read.

| Runbook | Failure domain | Operations queue (OPS-002) | Viewer slug (`/operations/runbooks/<slug>`) | Alert (deploy/prometheus/rules) |
|---|---|---|---|---|
| [connector.md](connector.md) | Connector / sync | `operations.queue.failedSync` | `connector-sync` | `ConnectorSyncFailureStreak` |
| [observation.md](observation.md) | Observation quality/freshness | `operations.queue.staleTargets` | `observation-freshness` | `BriefingGenerationFailure`, `ModelSpendBudgetExhausted` (triage) |
| [observation.md](observation.md) *(shared)* | Observation conflict | `operations.queue.conflicted` | `observation-conflict` | *(no owning alert)* |
| [observation.md](observation.md) *(shared)* | Identity mapping review | `operations.queue.identityMapping` | `identity-mapping` | *(no owning alert)* |
| [parser.md](parser.md) | Route C parser drift | `operations.queue.parserDrift` | `parser-drift` | `RouteCircuitOpen` |
| [action-reconciliation.md](action-reconciliation.md) | Action reconciliation | `operations.queue.pendingRecon` | `reconciliation` | `ReconciliationBacklog` |
| [llm-outage.md](llm-outage.md) | LLM / chat / briefing outage | *(no blocking queue by design)* — triage via `operations.queue.staleTargets` | — | `BriefingGenerationFailure`, `ModelSpendBudgetExhausted` |
| [digest-delivery.md](digest-delivery.md) | Daily email digest delivery | *(no blocking queue by design)* — triage via `operations.queue.staleTargets` | — | *(no owning alert)*; `BriefingGenerationFailure` is the adjacent signal |

`llm-outage.md` and `digest-delivery.md` own **no blocking queue by design** and so
carry no registry deep link: an LLM outage cannot reduce screen capability (§17.2),
and the daily digest is advisory UI whose items are all readable in-app under the
same shared event id (NOT-001). Both are reached from this index and from the
adjacent alerts, and both cite `operations.queue.staleTargets` for triage.

`observation.md` is the **shared** runbook for three observation-domain queues:
`staleTargets` (freshness), `conflicted` (Route A vs C conflict), and
`identityMapping` (identity-quarantine review). The identity-mapping queue owns no
dedicated alert and no dedicated file; it maps to the documented shared
observation runbook (smallest safe remediation, issue #159).

The queue keys are the locale catalog keys the Operations screen renders
(`apps/web/src/screens/Operations.tsx`). The registry `alerts` mirror the
`ops_queue` annotations in `deploy/prometheus/rules/dk-p0-alerts.yml` exactly (the
validator enforces the mirror); each alert also carries a `runbook` annotation
that points back here.

The dashboards referenced below live under `deploy/grafana/dashboards/` and are
listed in the DK P0 Grafana folder.

Release and deployment recovery is **not** a §20.1 failure domain and has no
runbook here. A bad release is rolled back by restoring the previous digests —
`task prod:rollback`, or `task release:images -- --tag <previous-version>` when
no snapshot exists — and the full procedure, including when a schema rollback is
and is not safe, is [`DEPLOYMENT.md`](../DEPLOYMENT.md) §11.

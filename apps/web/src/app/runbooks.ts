// Canonical Operations runbook registry (OPS-002 / PRD §20.1). The SINGLE source
// of truth mapping each Operations queue to its runbook destination:
//   - `slug`  — the in-SPA viewer identifier. A TECHNICAL id, LTR-isolated,
//               never localized (the same string in every locale).
//   - `file`  — the backing runbook markdown file (repo-relative). A file may be
//               SHARED by several queues (observation.md backs three).
//   - `alerts`— the Prometheus alert names whose `ops_queue` annotation routes an
//               operator to this queue (may be empty for a queue no alert owns).
//
// Operations.tsx derives its runbook links from here (no hardcoded `/docs/*`
// strings); the in-app viewer resolves slug→file→content from here; and
// deploy/grafana/validate_dashboards.py cross-checks the SAME mapping, so a
// renamed route, slug, file, or alert annotation fails `task obs:validate`.

export interface RunbookEntry {
  /** Operations queue key suffix — matches `operations.queue.<queue>`. */
  readonly queue: string;
  /** In-SPA viewer slug; a technical identifier, never translated. */
  readonly slug: string;
  /** Backing runbook markdown file, repo-relative. May be shared across queues. */
  readonly file: string;
  /** Prometheus alerts whose `ops_queue` annotation points at this queue. */
  readonly alerts: readonly string[];
}

// Keyed by the Operations queue key. `alerts` mirrors the `ops_queue` annotations
// in deploy/prometheus/rules/dk-p0-alerts.yml exactly (the validator enforces the
// mirror). `identityMapping` and `conflicted` own no alert and share the
// observation runbook — documented in runbooks/README.md.
export const RUNBOOKS = {
  failedSync: {
    queue: "failedSync",
    slug: "connector-sync",
    file: "runbooks/connector.md",
    alerts: ["ConnectorSyncFailureStreak"],
  },
  staleTargets: {
    queue: "staleTargets",
    slug: "observation-freshness",
    file: "runbooks/observation.md",
    alerts: ["BriefingGenerationFailure", "ModelSpendBudgetExhausted"],
  },
  identityMapping: {
    queue: "identityMapping",
    slug: "identity-mapping",
    file: "runbooks/observation.md",
    alerts: [],
  },
  conflicted: {
    queue: "conflicted",
    slug: "observation-conflict",
    file: "runbooks/observation.md",
    alerts: [],
  },
  parserDrift: {
    queue: "parserDrift",
    slug: "parser-drift",
    file: "runbooks/parser.md",
    alerts: ["RouteCircuitOpen"],
  },
  pendingRecon: {
    queue: "pendingRecon",
    slug: "reconciliation",
    file: "runbooks/action-reconciliation.md",
    alerts: ["ReconciliationBacklog"],
  },
} as const satisfies Record<string, RunbookEntry>;

export type RunbookQueueKey = keyof typeof RUNBOOKS;

/** Base path of the in-SPA runbook viewer route (registered in navConfig). */
export const RUNBOOK_BASE_PATH = "/operations/runbooks";

/** Deep link to the viewer for a slug. `to` is a technical path, never localized. */
export function runbookTo(slug: string): string {
  return `${RUNBOOK_BASE_PATH}/${slug}`;
}

/** Resolve a viewer slug back to its registry entry, or undefined if unknown. */
export function runbookBySlug(slug: string): RunbookEntry | undefined {
  return Object.values(RUNBOOKS).find((entry) => entry.slug === slug);
}

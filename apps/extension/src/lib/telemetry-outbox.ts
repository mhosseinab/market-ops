import { CONNECTOR_VERSION, SCHEMA_VERSION } from "./constants";
import type { MetricSample } from "./observability";
import { KEY_TELEMETRY_OUTBOX, type KeyValueStore } from "./storage";

// Durable operational-telemetry outbox (issue #162).
//
// MV3 service workers are suspended/restarted routinely, erasing the in-memory
// metric registry (observability.ts) before any operational sink ever sees it.
// This module persists a BOUNDED, ALLOW-LISTED snapshot of that registry into
// chrome.storage so extraction/delivery health survives worker lifecycle churn,
// and exports batches through a SINGLE injectable transport with idempotent batch
// ids (exactly-once-logical under retry).
//
// CONTAINMENT (never-cut: free-text containment + localization boundary applied
// to telemetry): only bounded parser/version/status labels + counts leave the
// extension. URLs, titles, credentials, raw marketplace text, and Persian copy
// are NEVER exported and NEVER enter a label — sanitizeMetrics() strips any label
// key outside the allowlist and any label value that is not a bounded token.
//
// FAIL-OPEN: export failure/unavailability NEVER blocks or degrades capture —
// batches stay durably pending within the bounded cap (the lowest-priority,
// shed-first class), and a throwing transport is treated as a transient retry.
//
// ENDPOINT STATUS (BLOCKED-on-endpoint): the gateway contract
// (contracts/gateway.openapi.yaml) exposes NO capture-credential-scoped telemetry
// ingest route today (only /observation/capture, /ext/pairing/*,
// /ext/owned-targets). Inventing one is a multi-plane api_data_contracts + server
// ingest decision, out of scope here. So the durable extension-side seam is
// complete and the transport is injectable; the DEFAULT transport keeps batches
// pending (unavailableTelemetryTransport). Wiring a real transport is a one-line
// swap once the endpoint shape is decided.

// The only label KEYS that may leave the extension. Everything else is a
// dimension we have not vetted for cardinality/PII — dropped, fail closed. These
// mirror the labels emitted across content-script/service-worker/schedule.
const ALLOWED_LABEL_KEYS = new Set([
  "capability",
  "endpoint",
  "kind",
  "outcome",
  "page",
  "reason",
  "status",
  "to",
]);

// A bounded, non-sensitive label VALUE token: ASCII, no whitespace, no slashes,
// length-capped. This is the containment guard that rejects a URL (contains "/"),
// a title or raw marketplace text (contains spaces / non-ASCII / is long), Persian
// copy (non-ASCII), and any credential-looking blob (too long). A value that
// fails is dropped, not exported.
const SAFE_LABEL_VALUE = /^[A-Za-z0-9_.:@-]{1,48}$/;

// The maximum number of durable telemetry batches. Telemetry is advisory (the
// lowest load-shedding priority), so beyond the cap the OLDEST batch is shed —
// bounded storage, never unbounded growth.
export const TELEMETRY_OUTBOX_CAP = 50;

export { KEY_TELEMETRY_OUTBOX };

// One exportable metric: a fixed metric name, its kind, allow-listed bounded
// labels, and a numeric value/count.
export interface TelemetryMetric {
  name: string;
  kind: "counter" | "gauge";
  labels: Record<string, string>;
  value: number;
}

// One durable, self-describing batch. batchId is a deterministic content hash so a
// replayed batch (crash-after-send, lost ack) carries the SAME id and the backend
// can dedupe it — exactly-once-logical under retry. connectorVersion/schemaVersion
// stamp the batch so a drift is attributable.
export interface TelemetryBatch {
  batchId: string;
  capturedAt: string;
  connectorVersion: string;
  schemaVersion: number;
  metrics: TelemetryMetric[];
}

// The export result maps to how a batch is treated: accepted → drain; retry →
// keep durably pending (transient failure / no endpoint yet).
export type TelemetryExportOutcome = "accepted" | "retry";

// The SINGLE injectable transport seam. A real implementation would POST the
// batch to a capture-credential-scoped telemetry endpoint; until that contract
// exists, the default keeps batches pending.
export type TelemetryTransport = (batch: TelemetryBatch) => Promise<TelemetryExportOutcome>;

// unavailableTelemetryTransport is the fail-open default while no telemetry
// endpoint exists (BLOCKED-on-endpoint). It never accepts, so batches stay
// durably pending within the bounded cap and capture is never affected.
export const unavailableTelemetryTransport: TelemetryTransport = async () => "retry";

// sanitizeMetrics applies the allowlist: drop any label key not in
// ALLOWED_LABEL_KEYS and any label value that is not a bounded token. The metric
// name and count always survive (they are non-sensitive by construction). A
// stripped label collapses cardinality — that is the intended fail-closed
// posture, not a bug.
export function sanitizeMetrics(samples: MetricSample[]): TelemetryMetric[] {
  return samples.map((s) => ({
    name: s.name,
    kind: s.kind,
    labels: sanitizeLabels(s.labels),
    value: s.value,
  }));
}

function sanitizeLabels(labels: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(labels)) {
    if (!ALLOWED_LABEL_KEYS.has(k)) continue;
    if (typeof v !== "string" || !SAFE_LABEL_VALUE.test(v)) continue;
    out[k] = v;
  }
  return out;
}

export interface SnapshotResult {
  appended: boolean;
  batchId: string;
  shed: boolean;
}

export interface ExportResult {
  accepted: number;
  retried: number;
  remaining: number;
}

// TelemetryOutbox is the durable, bounded snapshot/outbox. It is store-backed so a
// new instance over the same chrome.storage.local (a worker respawn) transparently
// restores every unshipped batch — durability is a property of the store, not of
// any in-memory field.
export class TelemetryOutbox {
  constructor(private store: KeyValueStore) {}

  private async load(): Promise<TelemetryBatch[]> {
    return (await this.store.get<TelemetryBatch[]>(KEY_TELEMETRY_OUTBOX)) ?? [];
  }

  private async save(batches: TelemetryBatch[]): Promise<void> {
    await this.store.set(KEY_TELEMETRY_OUTBOX, batches);
  }

  async count(): Promise<number> {
    return (await this.load()).length;
  }

  // pending returns the durable, unshipped batches — the authoritative restore
  // point after a worker restart.
  async pending(): Promise<TelemetryBatch[]> {
    return await this.load();
  }

  // snapshot sanitizes the current metric samples and appends ONE durable batch.
  // An empty snapshot (nothing recorded, e.g. right after a cold boot) is a no-op
  // — it never persists an empty batch. Append is idempotent: an identical metric
  // state at the same capturedAt yields the same batchId and is not duplicated.
  // Enforces the bounded cap by shedding the oldest batch.
  async snapshot(samples: MetricSample[], capturedAt: string): Promise<SnapshotResult> {
    const metrics = sanitizeMetrics(samples);
    if (metrics.length === 0) return { appended: false, batchId: "", shed: false };

    const batch: TelemetryBatch = {
      batchId: batchIdFor(metrics, capturedAt),
      capturedAt,
      connectorVersion: CONNECTOR_VERSION,
      schemaVersion: SCHEMA_VERSION,
      metrics,
    };

    const batches = await this.load();
    if (batches.some((b) => b.batchId === batch.batchId)) {
      // Idempotent append: this exact batch is already durably pending.
      return { appended: false, batchId: batch.batchId, shed: false };
    }
    batches.push(batch);
    let shed = false;
    while (batches.length > TELEMETRY_OUTBOX_CAP) {
      batches.shift();
      shed = true;
    }
    await this.save(batches);
    return { appended: true, batchId: batch.batchId, shed };
  }

  // export attempts to deliver every pending batch through the injected transport.
  // A batch is drained ONLY on an explicit "accepted" ack (exactly-once-logical:
  // the server dedupes a replayed batchId). Any other outcome — "retry" or a
  // thrown transport (network down, no endpoint) — keeps the batch durably
  // pending. It NEVER throws: telemetry export is fail-open and must never block
  // or degrade capture.
  async export(transport: TelemetryTransport): Promise<ExportResult> {
    const batches = await this.load();
    const remaining: TelemetryBatch[] = [];
    const res: ExportResult = { accepted: 0, retried: 0, remaining: 0 };

    for (const batch of batches) {
      let outcome: TelemetryExportOutcome;
      try {
        outcome = await transport(batch);
      } catch {
        outcome = "retry"; // fail-open: a broken transport never loses the batch
      }
      if (outcome === "accepted") {
        res.accepted++;
      } else {
        remaining.push(batch);
        res.retried++;
      }
    }

    await this.save(remaining);
    res.remaining = remaining.length;
    return res;
  }
}

// batchIdFor is a deterministic content hash of the batch payload — a REPLAY of
// the same stored batch produces the same id so the backend can dedupe it. It is
// an idempotency key, not a security primitive (same fnv1a the capture dedup key
// uses). Labels are sorted so key order never changes the hash.
function batchIdFor(metrics: TelemetryMetric[], capturedAt: string): string {
  const canonical = metrics
    .map((m) => {
      const labels = Object.entries(m.labels)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => `${k}=${v}`)
        .join(",");
      return `${m.name}|${m.kind}|${labels}|${m.value}`;
    })
    .join(";");
  return fnv1a(`${capturedAt}#${canonical}`);
}

function fnv1a(input: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

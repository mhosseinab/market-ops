import { CONNECTOR_VERSION, SCHEMA_VERSION } from "./constants";

// Observability (docs/14). Structured, LOCAL-FIRST logs with stable keys and NO
// PII / no raw marketplace free text / no credential. Every log carries the
// crawlRunId, connectorVersion, and schemaVersion so an extraction can be traced.
// Counters track extraction success per page type, missing critical fields, HTTP
// status by endpoint, selector failures, response key-set drift, queue depth, and
// batch upload latency/failure — the metrics named in docs/14.

export type MetricName =
  | "extraction_success"
  | "extraction_missing_field"
  | "parser_drift"
  | "selector_failure"
  | "http_status"
  | "queue_depth"
  | "queue_backpressure"
  | "upload_accepted"
  | "upload_failed"
  // Exhausted transient delivery moved to the durable dead-letter store (issue
  // #150) — a THIRD outcome, distinct from queue_backpressure (cap shed) and
  // upload_failed (permanent 4xx drop).
  | "upload_dead_letter"
  | "dead_letter_retry"
  | "dead_letter_discard"
  | "capability_transition"
  // The content script's capability-before-fetch gate (issue #155): a product
  // read that was refused because capture is not READY (unknown/disabled/revoked).
  // This is the observable proof that the fail-closed gate ran BEFORE any
  // marketplace product-endpoint request (PRD §4.6 "Unknown never enables").
  | "capture_gated"
  | "on_demand_latency_ms"
  | "watchlist_add"
  // The credential-scoped Confirmed-owned-target sync (#145, GET
  // /ext/owned-targets): `owned_targets_sync{outcome}` records ok vs. a
  // fail-closed clear (unavailable), and `owned_targets_count` gauges the
  // current projected target count.
  | "owned_targets_sync"
  | "owned_targets_count"
  | "schedule_cycle"
  | "schedule_request_denied"
  | "schedule_circuit_stop";

export interface LogFields {
  [key: string]: string | number | boolean | null | undefined;
}

// A stable per-boot crawl run id ties an extraction session's logs together.
export const crawlRunId = cryptoRandomId();

// counters are the local-first metric store. In production a periodic flush would
// ship these; the point here is that tests and prod share the SAME field names.
const counters = new Map<string, number>();
// gauges hold the CURRENT value of a point-in-time metric (e.g. queue depth) —
// distinct from counters: a gauge is SET to the latest observed value, never
// accumulated, so it always reads the real current state, not a running sum.
const gauges = new Map<string, number>();

export function metricKey(name: MetricName, labels: LogFields = {}): string {
  const parts = Object.entries(labels)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${String(v)}`);
  return [name, ...parts].join("|");
}

export function incr(name: MetricName, labels: LogFields = {}, by = 1): void {
  const key = metricKey(name, labels);
  counters.set(key, (counters.get(key) ?? 0) + by);
}

export function counterValue(name: MetricName, labels: LogFields = {}): number {
  return counters.get(metricKey(name, labels)) ?? 0;
}

// gauge sets a point-in-time metric to its REAL current value (e.g. the actual
// pending-queue length read fresh from storage) — never a placeholder constant.
export function gauge(name: MetricName, value: number, labels: LogFields = {}): void {
  gauges.set(metricKey(name, labels), value);
}

export function gaugeValue(name: MetricName, labels: LogFields = {}): number {
  return gauges.get(metricKey(name, labels)) ?? 0;
}

export function resetCounters(): void {
  counters.clear();
  gauges.clear();
}

// A structured, transport-ready view of one metric in the registry. Distinct from
// the flat metricKey string form: name and labels are separated so a durable
// telemetry outbox can allow-list dimensions before anything leaves the extension
// (issue #162). Values are stringified exactly as metricKey encoded them.
export interface MetricSample {
  name: string;
  kind: "counter" | "gauge";
  labels: Record<string, string>;
  value: number;
}

// snapshotMetrics reads the WHOLE in-memory registry into structured samples. It
// is a pure read — it never clears the maps — so the caller decides drain
// semantics (the outbox persists a durable copy; the live registry keeps serving
// popup/telemetry). The reverse of metricKey(): the first "|" segment is the
// metric name and each remaining "k=v" segment is one label.
export function snapshotMetrics(): MetricSample[] {
  const out: MetricSample[] = [];
  for (const [key, value] of counters) out.push({ ...parseMetricKey(key), kind: "counter", value });
  for (const [key, value] of gauges) out.push({ ...parseMetricKey(key), kind: "gauge", value });
  return out;
}

function parseMetricKey(key: string): { name: string; labels: Record<string, string> } {
  const [name, ...pairs] = key.split("|");
  const labels: Record<string, string> = {};
  for (const pair of pairs) {
    const eq = pair.indexOf("=");
    if (eq === -1) continue;
    labels[pair.slice(0, eq)] = pair.slice(eq + 1);
  }
  return { name: name ?? key, labels };
}

// log emits one structured record. It NEVER logs Persian-language copy as a
// diagnostic identifier (LOC boundary) — callers pass stable tokens only.
export function log(level: "info" | "warn" | "error", event: string, fields: LogFields = {}): void {
  const record = {
    level,
    event,
    crawlRunId,
    connectorVersion: CONNECTOR_VERSION,
    schemaVersion: SCHEMA_VERSION,
    ...fields,
  };
  // eslint-disable-next-line no-console -- structured local-first sink
  console[level === "error" ? "error" : level === "warn" ? "warn" : "log"](JSON.stringify(record));
}

function cryptoRandomId(): string {
  const c = (globalThis as { crypto?: Crypto }).crypto;
  if (c && "randomUUID" in c) return c.randomUUID();
  return `run-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e9).toString(36)}`;
}

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
  | "capability_transition";

export interface LogFields {
  [key: string]: string | number | boolean | null | undefined;
}

// A stable per-boot crawl run id ties an extraction session's logs together.
export const crawlRunId = cryptoRandomId();

// counters are the local-first metric store. In production a periodic flush would
// ship these; the point here is that tests and prod share the SAME field names.
const counters = new Map<string, number>();

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

export function resetCounters(): void {
  counters.clear();
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

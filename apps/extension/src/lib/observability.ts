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
  // The EXT-009 kill switch's SERVER-side revocation (issue #149).
  // `credential_revocation{outcome}` never carries the credential secret. The
  // outcome vocabulary is deliberately fine-grained so telemetry can always
  // distinguish a REAL revocation from a local resolution of one:
  //   confirmed             — the authority invalidated the credential, on
  //                           POSITIVE PROOF only (issue #149 fix 3): a 204, or
  //                           a 401 carrying the authority's own
  //                           CAPTURE_CREDENTIAL_INVALID verdict. A generic
  //                           proxy / pre-rollout 401, a 404/405, or any other
  //                           2xx is NOT proof and never lands here;
  //   pending               — no authoritative answer; retried under backoff;
  //   deferred              — a retry was skipped because it is inside its
  //                           backoff window (bounded load, not a drop);
  //   expiry_unverified     — an attempt was made and came back NON-authoritative
  //                           while the credential also looks expired by the
  //                           DEVICE clock. The clock is not authoritative, so
  //                           the revoke stays pending; this records that the
  //                           shortcut was REFUSED. The device clock can never
  //                           produce a terminal `revoked` (issue #149, G1);
  //   quarantined_unconfirmed
  //                         — a bound was reached without the authority ever
  //                           confirming: the CLOCK-INDEPENDENT attempt budget
  //                           (REVOCATION_MAX_ATTEMPTS) or the marker's durable
  //                           AGE bound. The revoke moves into the explicit
  //                           "could not confirm" QUARANTINE — capability
  //                           `revocation_unconfirmed`, deliberately NOT
  //                           `revoked` (nothing confirmed the server row is
  //                           dead) and NOT a silent discard (the credential is
  //                           retained and the revoke keeps retrying). It
  //                           REPLACES the former `abandoned_unconfirmed`, which
  //                           destroyed the credential and so guaranteed the
  //                           server row could never be killed;
  //   quarantine_deferred   — a quarantined retry was inside its backoff window;
  //   quarantine_repeat     — the user pressed Revoke AGAIN while a revoke was
  //                           quarantined (the quarantine holds the material, so
  //                           KEY_CREDENTIAL is already gone). It forces one
  //                           immediate retry and leaves the state at
  //                           `revocation_unconfirmed`. Distinct from
  //                           `already_cleared` on purpose: folding the two lost
  //                           the difference between an idempotent repeat of a
  //                           CONFIRMED revoke and one the authority never
  //                           confirmed;
  //   quarantine_evicted    — the bounded quarantine list was at its cap and the
  //                           OLDEST outstanding revocation was dropped. A
  //                           revocation we can no longer pursue, so it is
  //                           counted and warn-logged with the evicted
  //                           credentialId (never the secret);
  //   quarantine_retry_pending
  //                         — a quarantined retry ran and was still not
  //                           authoritative;
  //   confirmed_after_quarantine
  //                         — the authority finally confirmed a QUARANTINED
  //                           revoke. Distinct from `confirmed`: a revocation
  //                           confirmed late is not the same operational event
  //                           as one confirmed on the spot;
  //   quarantine_expired    — a quarantined revoke reached the credential's
  //                           authoritative expiry, so the credential can no
  //                           longer authenticate anything and the record is
  //                           discarded. Lands on `unknown`, NEVER `revoked`;
  //   forced_no_contact     — a USER-forced retry never reached the server, so it
  //                           did not consume the authoritative attempt budget
  //                           (an offline user pressing Pair must not end their
  //                           own revoke). The marker's AGE bound still applies;
  //   local_storage_error   — a user Revoke's DURABLE writes failed (e.g.
  //                           QUOTA_BYTES) and NOTHING durable was possible even
  //                           after shedding advisory telemetry. Capture is gated
  //                           OFF in memory for this worker lifetime only, and the
  //                           popup still gets a response; counted so the kill
  //                           switch never fails open silently;
  //   local_storage_error_recovered
  //                         — same failure, but the small fail-closed writes
  //                           landed on retry (after shedding the telemetry
  //                           outbox), so the revoke stays durably retryable and
  //                           capture stays off across a worker restart;
  //   local_storage_error_discarded
  //                         — same failure, and even those writes rejected, so
  //                           the credential material (and the stale stored
  //                           capability) were REMOVED — `remove` frees quota —
  //                           leaving a respawned worker nothing to capture with.
  //                           A last-resort fail-closed discard: the SERVER-side
  //                           revoke can no longer be pursued, which is why it is
  //                           its own outcome plus a warn log. Lands on `unknown`,
  //                           never `revoked`;
  //   retry_error           — a storage failure aborted a retry. The durable
  //                           marker is untouched, so the next due tick retries;
  //                           counted so the abort is never silent;
  //   orphaned              — the pending marker's credential material is gone,
  //                           so no retry can ever succeed. A path to `revoked`
  //                           without a server confirmation, hence its own
  //                           outcome. Emitted by BOTH the timer-driven retry and
  //                           the user-driven revoke, which reach it identically;
  //   already_cleared       — a revoke arrived with no marker and no credential:
  //                           an idempotent repeat of one that already completed,
  //                           distinct from a genuine `orphaned` resolution;
  //   marker_reconstructed  — a `revocation_pending` capability was found with no
  //                           durable marker and the marker was rebuilt from the
  //                           stored credential, so the revoke is not stranded.
  | "credential_revocation"
  // The content script's capability-before-fetch gate (issue #155): a product
  // read that was refused because capture is not READY (unknown/disabled/revoked).
  // This is the observable proof that the fail-closed gate ran BEFORE any
  // marketplace product-endpoint request (PRD §4.6 "Unknown never enables").
  | "capture_gated"
  | "on_demand_latency_ms"
  | "watchlist_add"
  // The credential-scoped Confirmed-owned-target sync (#145, GET
  // /ext/owned-targets): `owned_targets_sync{outcome}` records ok, a fail-closed
  // clear after a failed read (unavailable), a stale completion (stale), or a
  // sync REFUSED before any request because capture is not ready — including an
  // unconfirmed revocation (not_ready, issue #149). `owned_targets_count` gauges
  // the current projected target count.
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

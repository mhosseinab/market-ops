import { beforeEach, describe, expect, it, vi } from "vitest";
import type { MetricSample } from "./observability";
import { MemoryStore } from "./storage";
import {
  KEY_TELEMETRY_OUTBOX,
  sanitizeMetrics,
  TELEMETRY_OUTBOX_CAP,
  type TelemetryBatch,
  TelemetryOutbox,
  type TelemetryTransport,
} from "./telemetry-outbox";

// A metric sample as observability.snapshotMetrics() would emit one.
function sample(over: Partial<MetricSample>): MetricSample {
  return { name: "upload_accepted", kind: "counter", labels: {}, value: 1, ...over };
}

const NOW = "2026-07-20T10:00:00.000Z";

describe("telemetry-outbox — PII / secret containment (never-cut: free-text + locale boundary)", () => {
  it("keeps ONLY allow-listed bounded label keys with bounded-token values", () => {
    const metrics = sanitizeMetrics([
      sample({ name: "http_status", labels: { endpoint: "product", status: "404" } }),
    ]);
    expect(metrics).toEqual([
      {
        name: "http_status",
        kind: "counter",
        labels: { endpoint: "product", status: "404" },
        value: 1,
      },
    ]);
  });

  it("drops a URL/title/credential/raw-text label value — never lets it into a label", () => {
    const metrics = sanitizeMetrics([
      sample({
        name: "http_status",
        labels: {
          // A URL, a long title, and a Persian free-text string must NEVER survive.
          endpoint: "https://www.digikala.com/product/dkp-123/secret?token=abc",
          reason: "response is not an object",
          status: "کالای موجود نیست",
        },
      }),
    ]);
    // The metric + count survive; every unsafe label value is stripped.
    expect(metrics).toEqual([{ name: "http_status", kind: "counter", labels: {}, value: 1 }]);
  });

  it("drops label KEYS outside the allowlist (fail closed on unknown dimensions)", () => {
    const metrics = sanitizeMetrics([
      sample({
        name: "extraction_success",
        labels: { user_name: "ali", sender: "reza", page: "product" },
      }),
    ]);
    expect(metrics).toEqual([
      { name: "extraction_success", kind: "counter", labels: { page: "product" }, value: 1 },
    ]);
  });

  it("no exported batch ever contains a URL, credential, or raw marketplace text anywhere", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    await outbox.snapshot(
      [
        sample({
          name: "http_status",
          labels: { endpoint: "https://api.digikala.com/v2/product/1/", status: "202" },
        }),
        sample({ name: "parser_drift", labels: { reason: "missing top-level 'data' key" } }),
        sample({ name: "capability_transition", labels: { to: "revoked" } }),
      ],
      NOW,
    );
    const sent: TelemetryBatch[] = [];
    const transport: TelemetryTransport = async (b) => {
      sent.push(b);
      return "accepted";
    };
    await outbox.export(transport);

    const serialized = JSON.stringify(sent);
    expect(serialized).not.toMatch(/digikala\.com/i);
    expect(serialized).not.toMatch(/https?:\/\//i);
    expect(serialized).not.toMatch(/data.*key/i);
    // The bounded, non-sensitive dimension still made it through.
    expect(serialized).toContain("capability_transition");
    expect(serialized).toContain("revoked");
  });
});

describe("telemetry-outbox — durability across MV3 worker restart", () => {
  it("a snapshot persisted to storage survives a new outbox instance (worker respawn)", async () => {
    const store = new MemoryStore();
    await new TelemetryOutbox(store).snapshot([sample({ value: 9 })], NOW);

    // Simulate a worker restart: brand-new outbox over the SAME durable store.
    const restored = new TelemetryOutbox(store);
    const pending = await restored.pending();
    expect(pending).toHaveLength(1);
    expect(pending[0]?.metrics).toContainEqual(
      expect.objectContaining({ name: "upload_accepted", value: 9 }),
    );
  });

  it("restores queue depth from AUTHORITATIVE state, never as an accumulated counter", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    // The authoritative durable queue currently holds 3 items after restart.
    await outbox.snapshot([sample({ name: "queue_depth", kind: "gauge", value: 3 })], NOW);
    const [batch] = await outbox.pending();
    const depth = batch?.metrics.find((m) => m.name === "queue_depth");
    // A gauge carries the CURRENT authoritative value, never a running sum.
    expect(depth).toEqual({ name: "queue_depth", kind: "gauge", labels: {}, value: 3 });
  });
});

describe("telemetry-outbox — bounded cap (backpressure, never unbounded growth)", () => {
  it("sheds the OLDEST batch beyond the cap and reports the shed", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    for (let i = 0; i < TELEMETRY_OUTBOX_CAP + 5; i++) {
      // Distinct capturedAt → distinct batch id each time.
      await outbox.snapshot(
        [sample({ value: i })],
        `2026-07-20T10:00:${String(i).padStart(2, "0")}.000Z`,
      );
    }
    expect(await outbox.count()).toBe(TELEMETRY_OUTBOX_CAP);
    const pending = await outbox.pending();
    // The earliest (value 0..4) were shed; the newest survive.
    expect(pending[0]?.metrics[0]?.value).toBe(5);
  });
});

describe("telemetry-outbox — exactly-once-logical export under retry (idempotent batch ids)", () => {
  it("keeps a batch durably pending on retry and drains it only once accepted, with a STABLE id", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    await outbox.snapshot([sample({ value: 2 })], NOW);
    const seenIds: string[] = [];

    // First export: transport signals transient failure → batch stays.
    await outbox.export(async (b) => {
      seenIds.push(b.batchId);
      return "retry";
    });
    expect(await outbox.count()).toBe(1);

    // Second export (retry): SAME batch id → server can dedupe → then accepted.
    await outbox.export(async (b) => {
      seenIds.push(b.batchId);
      return "accepted";
    });
    expect(await outbox.count()).toBe(0);

    // The retried batch carried the identical idempotency id both times.
    expect(seenIds).toHaveLength(2);
    expect(new Set(seenIds).size).toBe(1);
  });

  it("re-snapshotting an identical metric state does not duplicate the batch (idempotent append)", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    const metrics = [sample({ value: 2 })];
    await outbox.snapshot(metrics, NOW);
    await outbox.snapshot(metrics, NOW);
    expect(await outbox.count()).toBe(1);
  });

  it("export failure NEVER throws to the caller (fail-open for capture)", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    await outbox.snapshot([sample({})], NOW);
    const throwing: TelemetryTransport = async () => {
      throw new Error("network down");
    };
    // A thrown transport is treated as a transient retry, not a rejection.
    await expect(outbox.export(throwing)).resolves.toEqual(
      expect.objectContaining({ accepted: 0, remaining: 1 }),
    );
    expect(await outbox.count()).toBe(1);
  });
});

describe("telemetry-outbox — empty snapshots are no-ops", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("does not persist a batch when there are no metrics to export", async () => {
    const store = new MemoryStore();
    const outbox = new TelemetryOutbox(store);
    const r = await outbox.snapshot([], NOW);
    expect(r.appended).toBe(false);
    expect(await outbox.count()).toBe(0);
    expect(await store.get(KEY_TELEMETRY_OUTBOX)).toBeUndefined();
  });
});

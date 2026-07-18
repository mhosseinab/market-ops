import { describe, expect, it } from "vitest";
import { dedupKey } from "./dedup";
import { type UploadOutcome, UploadQueue } from "./queue";
import { MemoryStore } from "./storage";
import type { CaptureUpload } from "./types";

function capture(overrides: Partial<CaptureUpload> = {}): CaptureUpload {
  return {
    marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
    targetId: "22222222-2222-2222-2222-222222222222",
    nativeVariantId: 987654321,
    subRoute: "passive",
    sourceType: "public-web-endpoint",
    parserVersion: "dk-product@1.0.0",
    evidenceRef: "https://www.digikala.com/product/dkp-2345678/",
    availabilityStatus: "in_stock",
    capturedAt: "2026-07-18T10:00:00Z",
    confidence: "verified",
    price: { text: "125000000 IRR-rial", value: "125000000", unit: "IRR-rial" },
    ...overrides,
  };
}

describe("UploadQueue — idempotent offline retry (OBS-008, docs/09)", () => {
  it("produces a STABLE dedup key for a byte-identical replay", () => {
    expect(dedupKey(capture())).toBe(dedupKey(capture()));
    // A different captured instant is distinct evidence, not a replay.
    expect(dedupKey(capture())).not.toBe(dedupKey(capture({ capturedAt: "2026-07-18T11:00:00Z" })));
  });

  it("enqueuing an identical capture twice yields ONE pending item (no duplicate)", async () => {
    const q = new UploadQueue(new MemoryStore());
    const first = await q.enqueue(capture());
    const second = await q.enqueue(capture());
    expect(first).toEqual({ enqueued: true, deduped: false, shed: false });
    expect(second).toEqual({ enqueued: false, deduped: true, shed: false });
    expect(await q.count()).toBe(1);
  });

  it("replays after an offline failure until accepted, then removes the item", async () => {
    const store = new MemoryStore();
    const q = new UploadQueue(store);
    await q.enqueue(capture());

    // First flush: transient failure (offline) → item retried, still queued.
    let r = await q.flush(async () => "retry");
    expect(r.retried).toBe(1);
    expect(await q.count()).toBe(1);

    // Second flush: accepted → removed, zero remaining.
    r = await q.flush(async () => "accepted");
    expect(r.accepted).toBe(1);
    expect(await q.count()).toBe(0);
  });

  it("STOPS the flush and fails closed on a revoked outcome (EXT-001 kill switch)", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    await q.enqueue(capture({ capturedAt: "2026-07-18T11:00:00Z" }));
    const r = await q.flush(async () => "revoked");
    expect(r.revoked).toBe(true);
    expect(r.accepted).toBe(0);
    // Both items are retained (nothing delivered under a killed credential).
    expect(await q.count()).toBe(2);
  });

  it("parks an item after MAX_ATTEMPTS instead of retrying forever (bounded)", async () => {
    const q = new UploadQueue(new MemoryStore());
    await q.enqueue(capture());
    const always: () => Promise<UploadOutcome> = async () => "retry";
    for (let i = 0; i < 5; i++) await q.flush(always);
    expect(await q.count()).toBe(0); // parked-dropped, not looping
  });
});
